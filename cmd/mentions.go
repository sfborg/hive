package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

var mentionsCmd = &cobra.Command{
	Use:   "mentions",
	Short: "Name-mention index for finding papers by higher taxon",
	Long: `The name-mention index records how often each scientific name is
tagged in a paper and where NCBI Taxonomy and the Catalogue of Life
place it, so papers can be found by higher taxon before they are
assimilated. It is a discovery aid: its classification is never
imported into the archive's taxonomy.`,
}

var mentionsIndexCmd = &cobra.Command{
	Use:   "index <archive.db> [ref-id...]",
	Short: "Count tagged names per paper and verify them against NCBI Taxonomy and the Catalogue of Life",
	Long: `Reads the augmented JATS of each paper's attached documents, counts
the tagged scientific names, verifies the unique names against NCBI
Taxonomy and the Catalogue of Life through GNverifier, and replaces the
papers' rows in the name-mention index. Use --all to index every
reference.`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMentionsIndex,
}

func runMentionsIndex(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	refIDs := args[1:]
	if all == (len(refIDs) > 0) {
		return fmt.Errorf("give ref-ids or --all, not both or neither")
	}
	ctx := cmd.Context()
	a, err := hive.Open(args[0])
	if err != nil {
		return err
	}
	defer a.Close()
	out := cmd.OutOrStdout()

	if all {
		hits, _, err := a.ListReferencesPage(ctx, 0, 0, hive.ReferenceSortAuthor, hive.ReferenceQuery{})
		if err != nil {
			return err
		}
		for _, h := range hits {
			refIDs = append(refIDs, h.ID)
		}
	}

	read := func(sha string) ([]byte, error) {
		return sidecar.Read(a.Path(), sha, "jats.xml")
	}
	counts := make(map[string]map[string]int, len(refIDs))
	// authored[id][name] is the name with the authorship the paper
	// gives it, verified as well so its author score can pick among
	// homonyms.
	authored := make(map[string]map[string]string, len(refIDs))
	unique := make(map[string]bool)
	bare := make(map[string]bool)
	skipped := 0
	for _, id := range refIDs {
		scan, docs, err := a.NameMentionScan(ctx, id, read)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if docs == 0 {
			skipped++
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
	names := make([]string, 0, len(unique))
	for n := range unique {
		names = append(names, n)
	}
	sort.Strings(names)
	withAuthors := 0
	for _, byName := range authored {
		withAuthors += len(byName)
	}

	fmt.Fprintf(out, "Indexing %d unique name strings from %d papers (%d names with an authorship)",
		len(names), len(counts), withAuthors)
	if skipped > 0 {
		fmt.Fprintf(out, " (%d papers skipped: no augmented JATS)", skipped)
	}
	fmt.Fprintln(out, ".")
	for _, src := range mentions.Sources {
		v := mentions.NewVerifier(src.ID)
		tag, err := mentions.SourceTag(v, src.ID)
		if err != nil {
			return err
		}
		matches := mentions.Verify(ctx, v, names)
		for id, c := range counts {
			authors := map[string]hive.NameAuthorship{}
			for name, auth := range authored[id] {
				m, ok := matches[name]
				if !ok {
					continue
				}
				authors[name] = hive.NameAuthorship{Authorship: auth,
					Scores: mentions.AuthorScores(m, matches[name+" "+auth])}
			}
			if err := a.ReplaceNameMentionsWithAuthors(ctx, id, tag, c, matches, authors); err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
		}
		byType := map[string]int{}
		homonyms := 0
		authorMatched := 0
		for _, byName := range authored {
			for name, auth := range byName {
				for _, sc := range mentions.AuthorScores(matches[name], matches[name+" "+auth]) {
					if sc > 0 {
						authorMatched++
						break
					}
				}
			}
		}
		for _, n := range names {
			if !bare[n] {
				continue
			}
			if m, ok := matches[n]; ok {
				byType[m.MatchType]++
				if len(m.Homonyms) > 0 {
					homonyms++
				}
			} else {
				byType["NoMatch"]++
			}
		}
		types := make([]string, 0, len(byType))
		for t := range byType {
			types = append(types, t)
		}
		sort.Strings(types)
		fmt.Fprintf(out, "%s (%s), unique names by match type:\n", src.Label, tag)
		for _, t := range types {
			fmt.Fprintf(out, "  %-14s %d\n", t, byType[t])
		}
		fmt.Fprintf(out, "  %d names match more than one lineage (homonyms)\n", homonyms)
		fmt.Fprintf(out, "  %d of %d paper names with an authorship match a record's authors\n", authorMatched, withAuthors)
	}
	return nil
}

func init() {
	mentionsIndexCmd.Flags().Bool("all", false, "index every reference in the archive")
	mentionsCmd.AddCommand(mentionsIndexCmd)
	rootCmd.AddCommand(mentionsCmd)
}
