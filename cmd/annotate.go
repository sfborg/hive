package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/hive/pkg/taxonomy/annotator"
)

var annotateCmd = &cobra.Command{
	Use:   "annotate",
	Short: "Scientific-name tagging of attached documents",
}

var annotateRetagCmd = &cobra.Command{
	Use:   "retag <archive.db> [ref-id...]",
	Short: "Add name tags that earlier annotation passes missed",
	Long: `Updates the augmented JATS of the given references' attached documents
in place. It tags names in the title, abstract and keywords and in body
text containing XML entities, which earlier annotation passes did not
scan, moves brackets and punctuation that ended up inside a name tag
to just outside it, and declares the TaxPub namespace with Plazi's URI.
Existing tags are otherwise left as they are. New tags are verified and
abbreviations expanded through GNverifier.

Re-run "hive mentions index" afterwards to update the name-mention
index.`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runAnnotateRetag,
}

func runAnnotateRetag(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
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

	ann := annotator.New()
	tagOpen := []byte("<" + annotator.TaxPubPrefix + ":taxon-name ")
	var files, changed, before, after int
	var failed []string
	for _, id := range refIDs {
		shas, err := a.ReferencePDFs(ctx, id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		for _, sha := range shas {
			orig, err := sidecar.Read(a.Path(), sha, "jats.xml")
			if err != nil {
				return fmt.Errorf("%s: %w", sha, err)
			}
			if orig == nil {
				continue
			}
			files++
			doc, err := retag(ctx, ann, orig)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s (reference %s): %v", sha, id, err))
				continue
			}
			before += bytes.Count(orig, tagOpen)
			after += bytes.Count(doc, tagOpen)
			if bytes.Equal(doc, orig) {
				continue
			}
			changed++
			if dryRun {
				continue
			}
			if err := a.StoreJATS(ctx, sha, doc); err != nil {
				return fmt.Errorf("%s: store: %w", sha, err)
			}
		}
	}
	for _, f := range failed {
		fmt.Fprintln(cmd.ErrOrStderr(), "skipped "+f)
	}
	verb := "Updated"
	if dryRun {
		verb = "Would update"
	}
	fmt.Fprintf(out, "%s %d of %d documents; name tags %d → %d.", verb, changed, files, before, after)
	if len(failed) > 0 {
		fmt.Fprintf(out, " %d documents skipped (left unchanged).", len(failed))
	}
	fmt.Fprintln(out)
	if !dryRun && changed > 0 {
		fmt.Fprintf(out, "Update the name-mention index with: hive mentions index %s --all\n", args[0])
	}
	return nil
}

func init() {
	annotateRetagCmd.Flags().Bool("all", false, "retag every reference in the archive")
	annotateRetagCmd.Flags().Bool("dry-run", false, "report what would change without writing")
	annotateCmd.AddCommand(annotateRetagCmd)
	rootCmd.AddCommand(annotateCmd)
}

// retag runs the additive annotation passes over one augmented JATS
// document and checks that the result is still well-formed XML.
func retag(ctx context.Context, ann *annotator.Annotator, orig []byte) ([]byte, error) {
	doc, err := ann.AddMissedNames(orig)
	if err != nil {
		return nil, err
	}
	doc = annotator.TrimTagBoundaries(doc)
	doc = annotator.AlignTaxPubNamespace(doc)
	if doc, err = ann.VerifyFullNames(ctx, doc); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if doc, err = ann.ExpandAbbreviations(ctx, doc); err != nil {
		return nil, fmt.Errorf("expand abbreviations: %w", err)
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return doc, nil
			}
			return nil, fmt.Errorf("result is not well-formed XML: %w", err)
		}
	}
}
