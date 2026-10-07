// Package cmd — hive ner: run the NER prompt over one or more
// references. Input per ref is the reference's augmented JATS
// body; output persists to hive__extraction_run and materializes
// into hive__reference_assertion rows.
//
// Split from hive compose so a curator can evaluate NER
// separately (quality-check, model shopping) without paying
// for the downstream compose pass. Compose depends on NER — a
// separate hive compose command handles that phase.
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sfborg/hive/pkg/llm"
)

var nerCmd = &cobra.Command{
	Use:   "ner [ref-id...]",
	Short: "Run NER extraction over one or more references",
	Long: `Invokes the NER prompt against each reference's augmented JATS body
and materializes DwC-tagged assertions into hive__reference_assertion.

Selection:
  Ref-id arguments run only those references (validated up-front).
  --all runs the whole archive.

Guardrails:
  Hive does not cap spend. Spending limits must be set and monitored
  with your service provider — hive echoes provider-reported token
  counts but never a dollar figure.

See also:
  hive compose  — runs the compose prompt against a reference's NER
                  output (requires prior NER runs; --ner chains).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runNER(cmd, args)
	},
}

func runNER(cmd *cobra.Command, args []string) error {
	server, _ := cmd.Flags().GetString("server")
	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	effort, _ := cmd.Flags().GetString("effort")
	thinking, _ := cmd.Flags().GetBool("thinking-summary")
	prompt, _ := cmd.Flags().GetString("prompt")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	jsonOut, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	all, _ := cmd.Flags().GetBool("all")

	server = strings.TrimRight(server, "/")
	if provider == "" {
		return fmt.Errorf("--provider is required (see `hive ner --help`)")
	}
	if !llm.ValidEffort(effort) {
		return fmt.Errorf("--effort must be one of %s", strings.Join(llm.Efforts, ", "))
	}
	httpClient := &http.Client{Timeout: 30 * time.Minute}
	if prompt == "" {
		latest, err := latestPromptForPurpose(cmd.Context(), httpClient, server, "ner")
		if err != nil {
			return fmt.Errorf("resolve latest ner prompt: %w", err)
		}
		prompt = latest
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if all && len(args) > 0 {
		return fmt.Errorf("--all cannot be combined with explicit ref-id arguments")
	}
	if !all && len(args) == 0 {
		return fmt.Errorf("supply one or more ref-ids or use --all")
	}

	refs, err := resolveRefs(cmd.Context(), httpClient, server, args, all)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no references to process")
		return nil
	}

	opts := llmBatchOpts{
		HTTPClient:  httpClient,
		Server:      server,
		Refs:        refs,
		Provider:    provider,
		Model:       model,
		Effort:      effort,
		Thinking:    thinking,
		Prompt:      prompt,
		Purpose:     "ner",
		Concurrency: concurrency,
		JSONOut:     jsonOut,
		Quiet:       quiet,
		inputFn: func(ctx context.Context, refID string) (string, string, string, error) {
			jats, err := fetchAugmentedJATS(ctx, llmBatchOpts{
				HTTPClient: httpClient, Server: server,
			}, refID)
			if err != nil {
				return "", "", "", err
			}
			return jats, "", "", nil
		},
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Would run NER (prompt %q) against %d reference(s)\n"+
				"  Provider: %s\n  Model:    %s\n",
			prompt, len(refs), provider, modelLabel(model, effort))
		for _, id := range refs {
			fmt.Fprintln(cmd.OutOrStdout(), "  "+id)
		}
		return nil
	}
	if !yes {
		headline := fmt.Sprintf("Ready to run NER (prompt %q) over %d reference(s)",
			prompt, len(refs))
		if err := confirmRun(headline, provider, modelLabel(model, effort)); err != nil {
			return err
		}
	}
	return runBatch(cmd.Context(), opts)
}

func init() {
	nerCmd.Flags().String("server", "http://127.0.0.1:2365", "base URL of the running hive server")
	nerCmd.Flags().String("provider", "", "provider name (matches an entry in your llm_providers config)")
	nerCmd.Flags().String("model", "", "model override; empty means the provider's configured default")
	nerCmd.Flags().Bool("thinking-summary", false, "request a summary of the model's reasoning, stored on each run (Anthropic models that think by default only; no extra cost)")
	nerCmd.Flags().String("effort", "", "reasoning effort: low | medium | high | xhigh | max (Anthropic providers only); empty means the model default")
	nerCmd.Flags().String("prompt", "", "shipped prompt name (default: highest-versioned ner_v* prompt on the server)")
	nerCmd.Flags().Int("concurrency", 1, "number of parallel invoke calls; default 1 avoids accidentally slamming the provider")
	nerCmd.Flags().Bool("all", false, "run against every reference in the archive")
	nerCmd.Flags().Bool("yes", false, "skip the interactive confirmation prompt (for scripting)")
	nerCmd.Flags().Bool("dry-run", false, "preview the ref list, provider, and model; no LLM calls, no confirmation")
	nerCmd.Flags().Bool("json", false, "emit one NDJSON row per completed reference instead of the rolling progress line")
	nerCmd.Flags().Bool("quiet", false, "suppress the progress line and end-of-run summary")
	rootCmd.AddCommand(nerCmd)
}
