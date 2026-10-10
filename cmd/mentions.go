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
tagged in a paper and where the archive's name sources (by default NCBI
Taxonomy and the Catalogue of Life) place it, so papers can be found
by higher taxon before they are assimilated. It is a discovery aid: its classification is never
imported into the archive's taxonomy.`,
}

var mentionsIndexCmd = &cobra.Command{
	Use:   "index <archive.db> [ref-id...]",
	Short: "Count tagged names per paper and verify them against the archive's name sources",
	Long: `Reads the augmented JATS of each paper's attached documents, counts
the tagged scientific names, verifies the unique names through
GNverifier against the archive's name sources (chosen in the web
interface; NCBI Taxonomy and the Catalogue of Life by default), and
replaces the papers' rows in the name-mention index. Use --all to index every
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
		refIDs = nil
	}
	read := func(sha string) ([]byte, error) {
		return sidecar.Read(a.Path(), sha, "jats.xml")
	}
	newVerifier := func(id int) mentions.Verifier { return mentions.NewVerifier(id) }
	rep, err := a.IndexNameMentions(ctx, refIDs, read, newVerifier, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Indexed %d unique name strings from %d papers (%d names with an authorship)",
		rep.Names, rep.Papers, rep.WithAuthors)
	if rep.Skipped > 0 {
		fmt.Fprintf(out, " (%d papers skipped: no augmented JATS)", rep.Skipped)
	}
	fmt.Fprintln(out, ".")
	for _, sr := range rep.Sources {
		types := make([]string, 0, len(sr.ByType))
		for t := range sr.ByType {
			types = append(types, t)
		}
		sort.Strings(types)
		fmt.Fprintf(out, "%s (%s), unique names by match type:\n", sr.Source.Label, sr.Tag)
		for _, t := range types {
			fmt.Fprintf(out, "  %-14s %d\n", t, sr.ByType[t])
		}
		fmt.Fprintf(out, "  %d names match more than one lineage (homonyms)\n", sr.Homonyms)
		fmt.Fprintf(out, "  %d of %d paper names with an authorship match a record's authors\n",
			sr.AuthorMatched, rep.WithAuthors)
	}
	return nil
}

func init() {
	mentionsIndexCmd.Flags().Bool("all", false, "index every reference in the archive")
	mentionsCmd.AddCommand(mentionsIndexCmd)
	rootCmd.AddCommand(mentionsCmd)
}
