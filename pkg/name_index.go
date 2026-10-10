package hive

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// IndexProgress reports how far IndexNameMentions has got: Stage names
// the step ("Reading papers", "Verifying with NCBI Taxonomy", …), Done
// of Total counts its units.
type IndexProgress struct {
	Stage       string
	Done, Total int
}

// SourceIndexReport summarizes one name source's verification.
type SourceIndexReport struct {
	Source mentions.Source
	// Tag is the raw__source the rows were stored under.
	Tag string
	// ByType counts unique names by match type ("Exact", "NoMatch", …).
	ByType map[string]int
	// Homonyms counts names matching more than one lineage.
	Homonyms int
	// AuthorMatched counts paper names whose authorship matches a
	// record's authors.
	AuthorMatched int
}

// IndexReport summarizes an IndexNameMentions run.
type IndexReport struct {
	Papers      int // papers indexed
	Skipped     int // papers without augmented JATS
	Names       int // unique name strings verified
	WithAuthors int // paper names with an authorship
	Sources     []SourceIndexReport
}

// IndexNameMentions rebuilds the name-mention index for the given
// references (all when refIDs is nil) against the archive's name
// sources: it counts the tagged names in each paper's augmented JATS,
// verifies the unique names with each source, and replaces the papers'
// rows. Rows from sources the archive no longer uses are removed.
//
// read returns a document's augmented JATS (nil when it has none);
// newVerifier returns a verifier for one GNverifier data source.
// progress, when not nil, is called as the work advances.
func (a *Archive) IndexNameMentions(ctx context.Context, refIDs []string,
	read func(sha string) ([]byte, error),
	newVerifier func(sourceID int) mentions.Verifier,
	progress func(IndexProgress)) (*IndexReport, error) {
	report := func(stage string, done, total int) {
		if progress != nil {
			progress(IndexProgress{stage, done, total})
		}
	}
	sources, err := a.NameSources(ctx)
	if err != nil {
		return nil, err
	}
	if refIDs == nil {
		hits, _, err := a.ListReferencesPage(ctx, 0, 0, ReferenceSortAuthor, ReferenceQuery{})
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			refIDs = append(refIDs, h.ID)
		}
	}

	out := &IndexReport{}
	counts := make(map[string]map[string]int, len(refIDs))
	// authored[id][name] is the authorship the paper gives a name; the
	// name with it is verified too, so its author score can pick among
	// homonyms.
	authored := make(map[string]map[string]string, len(refIDs))
	unique := map[string]bool{}
	bare := map[string]bool{}
	for i, id := range refIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report("Reading papers", i, len(refIDs))
		scan, docs, err := a.NameMentionScan(ctx, id, read)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if docs == 0 {
			out.Skipped++
			continue
		}
		counts[id] = scan.Names
		authored[id] = map[string]string{}
		for name := range scan.Names {
			unique[name] = true
			bare[name] = true
			if auth := mentions.PickAuthorship(scan.Authors[name]); auth != "" {
				authored[id][name] = auth
				unique[name+" "+auth] = true
			}
		}
	}
	report("Reading papers", len(refIDs), len(refIDs))
	names := make([]string, 0, len(unique))
	for n := range unique {
		names = append(names, n)
	}
	sort.Strings(names)
	out.Papers, out.Names = len(counts), len(names)
	for _, byName := range authored {
		out.WithAuthors += len(byName)
	}

	if err := a.dropOtherSourceMentions(ctx, refIDs, sources); err != nil {
		return nil, err
	}
	// Papers without JATS are not re-verified; refresh their summaries
	// so rows just dropped stop counting.
	for _, id := range refIDs {
		if _, indexed := counts[id]; !indexed {
			if err := a.WithTx(ctx, func(tx *Tx) error { return refreshTaxonSummary(tx, id) }); err != nil {
				return nil, err
			}
		}
	}
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report("Verifying with "+src.Label, 0, len(names))
		v := newVerifier(src.ID)
		tag, err := mentions.SourceTag(v, src.ID)
		if err != nil {
			return nil, err
		}
		matches := mentions.VerifyWithProgress(ctx, v, names, func(done, total int) {
			report("Verifying with "+src.Label, done, total)
		})
		done := 0
		for id, c := range counts {
			report("Storing "+src.Label+" matches", done, len(counts))
			done++
			authors := map[string]NameAuthorship{}
			for name, auth := range authored[id] {
				if m, ok := matches[name]; ok {
					authors[name] = NameAuthorship{Authorship: auth,
						Scores: mentions.AuthorScores(m, matches[name+" "+auth])}
				}
			}
			if err := a.ReplaceNameMentionsWithAuthors(ctx, id, tag, c, matches, authors); err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
		}
		sr := SourceIndexReport{Source: src, Tag: tag, ByType: map[string]int{}}
		for _, byName := range authored {
			for name, auth := range byName {
				for _, sc := range mentions.AuthorScores(matches[name], matches[name+" "+auth]) {
					if sc > 0 {
						sr.AuthorMatched++
						break
					}
				}
			}
		}
		for n := range bare {
			if m, ok := matches[n]; ok {
				sr.ByType[m.MatchType]++
				if len(m.Homonyms) > 0 {
					sr.Homonyms++
				}
			} else {
				sr.ByType["NoMatch"]++
			}
		}
		out.Sources = append(out.Sources, sr)
	}
	report("Done", 1, 1)
	return out, nil
}

// dropOtherSourceMentions deletes the references' mention rows from
// sources other than the given ones.
func (a *Archive) dropOtherSourceMentions(ctx context.Context, refIDs []string, sources []mentions.Source) error {
	if len(refIDs) == 0 {
		return nil
	}
	keep := make([]string, len(sources))
	args := make([]any, 0, len(sources)+len(refIDs))
	for i, s := range sources {
		keep[i] = "raw__source NOT LIKE ?"
		args = append(args, mentions.TagPrefix(s.ID)+"%")
	}
	return a.WithTx(ctx, func(tx *Tx) error {
		for start := 0; start < len(refIDs); start += 500 {
			batch := refIDs[start:min(start+500, len(refIDs))]
			in, ids := inClause(batch)
			if _, err := tx.tx.ExecContext(tx.ctx,
				`DELETE FROM hive__name_mention WHERE reference_id IN (`+in+`) AND `+strings.Join(keep, " AND "),
				append(append([]any{}, ids...), args...)...); err != nil {
				return fmt.Errorf("core: drop mentions from removed sources: %w", err)
			}
		}
		return nil
	})
}
