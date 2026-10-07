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
tagged in a paper and where the Catalogue of Life places it, so papers
can be found by higher taxon before they are assimilated. It is a
discovery aid: its classification is never imported into the archive's
taxonomy.`,
}

var mentionsIndexCmd = &cobra.Command{
	Use:   "index <archive.db> [ref-id...]",
	Short: "Count tagged names per paper and verify them against the Catalogue of Life",
	Long: `Reads the augmented JATS of each paper's attached documents, counts
the tagged scientific names, verifies the unique names against the
Catalogue of Life through GNverifier, and replaces the papers' rows in
the name-mention index. Use --all to index every reference.`,
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
	unique := make(map[string]bool)
	skipped := 0
	for _, id := range refIDs {
		c, docs, err := a.NameMentionCounts(ctx, id, read)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if docs == 0 {
			skipped++
			continue
		}
		counts[id] = c
		for name := range c {
			unique[name] = true
		}
	}
	names := make([]string, 0, len(unique))
	for n := range unique {
		names = append(names, n)
	}
	sort.Strings(names)

	v := mentions.NewVerifier()
	source, err := mentions.SourceTag(v, mentions.CatalogueOfLife)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Verifying %d unique names from %d papers against %s…\n", len(names), len(counts), source)
	matches := mentions.Verify(ctx, v, names)

	byType := map[string]int{}
	for _, n := range names {
		if m, ok := matches[n]; ok {
			byType[m.MatchType]++
		} else {
			byType["NoMatch"]++
		}
	}
	for id, c := range counts {
		if err := a.ReplaceNameMentions(ctx, id, source, c, matches); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}

	fmt.Fprintf(out, "Indexed %d papers", len(counts))
	if skipped > 0 {
		fmt.Fprintf(out, " (%d skipped: no augmented JATS)", skipped)
	}
	fmt.Fprintln(out, ".")
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	fmt.Fprintln(out, "Unique names by match type:")
	for _, t := range types {
		fmt.Fprintf(out, "  %-14s %d\n", t, byType[t])
	}
	return nil
}

func init() {
	mentionsIndexCmd.Flags().Bool("all", false, "index every reference in the archive")
	mentionsCmd.AddCommand(mentionsIndexCmd)
	rootCmd.AddCommand(mentionsCmd)
}
