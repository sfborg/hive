package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/assembler"
	"github.com/spf13/cobra"
)

var reassembleCmd = &cobra.Command{
	Use:           "reassemble <archive.db>",
	Short:         "Rebuild the literature view (Layer 2) from Layer 1 assertions",
	Long: `Rebuild the literature view (Layer 2) from Layer 1 assertions.

Loads every compose extraction run in the archive in deterministic order
(created_at ascending, run_id as tiebreak), runs the assembler across all
paper assertions, and materializes matched / newly-created taxa, synonyms,
[bracket]ed species, and sensu-derived concept relations into the taxon /
name / synonym / taxon_concept_relation tables.

See docs/2026-09-12_LITERATURE_VIEW.md for the four-layer model and Slice 1
scope.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		asJSON, _ := cmd.Flags().GetBool("json")

		a, err := hive.Open(args[0])
		if err != nil {
			return err
		}
		defer a.Close()

		var report *hive.ReassembleReport
		err = a.WithTx(context.Background(), func(tx *hive.Tx) error {
			out, err := tx.ExecuteReassemble(assembler.DefaultRules())
			report = out
			return err
		})
		if err != nil {
			return err
		}
		if asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		if !quiet {
			printReassembleReport(report)
		}
		return nil
	},
}

func printReassembleReport(r *hive.ReassembleReport) {
	fmt.Fprintf(os.Stdout, "papers loaded:   %d\n", r.PapersLoaded)
	fmt.Fprintf(os.Stdout, "papers skipped:  %d\n", r.PapersSkipped)
	fmt.Fprintf(os.Stdout, "matched:         %d\n", r.AssemblerReport.Matched)
	fmt.Fprintf(os.Stdout, "taxa created:    %d\n", r.AssemblerReport.TaxaCreated)
	fmt.Fprintf(os.Stdout, "synonyms:        %d\n", r.AssemblerReport.SynonymsCreated)
	fmt.Fprintf(os.Stdout, "bracketed:       %d\n", r.AssemblerReport.BracketedCreated)
	fmt.Fprintf(os.Stdout, "concept rels:    %d\n", r.AssemblerReport.ConceptRelationsInserted)
	fmt.Fprintf(os.Stdout, "concept fields:  %d\n", r.AssemblerReport.ConceptFieldsSet)
	fmt.Fprintf(os.Stdout, "type material:   %d\n", r.TypeMaterialInserted)
	fmt.Fprintf(os.Stdout, "distributions:   %d\n", r.DistributionInserted)
	fmt.Fprintf(os.Stdout, "vernaculars:     %d\n", r.VernacularInserted)
	fmt.Fprintf(os.Stdout, "extensions replaced: %d\n", r.ExtensionRowsReplaced)
	if len(r.Warnings) > 0 {
		fmt.Fprintln(os.Stdout, "\nwarnings:")
		for _, w := range r.Warnings {
			fmt.Fprintf(os.Stdout, "  - %s\n", w)
		}
	}
	if len(r.AssemblerReport.Flags) > 0 {
		fmt.Fprintln(os.Stdout, "\nflags:")
		for _, f := range r.AssemblerReport.Flags {
			fmt.Fprintf(os.Stdout, "  - [%d] %s\n", f.Kind, f.Message)
		}
	}
}

func init() {
	reassembleCmd.Flags().Bool("quiet", false, "suppress the human-readable summary")
	reassembleCmd.Flags().Bool("json", false, "emit the report as JSON on stdout")
	rootCmd.AddCommand(reassembleCmd)
}
