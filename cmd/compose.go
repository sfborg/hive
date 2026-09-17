// Package cmd — hive compose: run the compose prompt over one or
// more references. Input per ref is a two-section message —
// the reference's augmented JATS body followed by the raw output
// of the reference's most-recent NER run. Output persists to
// hive__extraction_run and links back via parent_run_id.
//
// A reference with no NER run is loudly skipped by default (a
// compose call with no NER assertions would burn tokens on an
// empty compose target). --ner runs NER first for such refs so
// the batch can complete in one command.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var composeCmd = &cobra.Command{
	Use:   "compose [ref-id...]",
	Short: "Run compose over one or more references (requires prior NER)",
	Long: `Invokes the compose prompt against each reference's augmented JATS
body plus the raw output of its most-recent NER run. Persists to
hive__extraction_run linked to the NER run via parent_run_id.

Selection:
  Ref-id arguments run only those references (validated up-front).
  --all runs the whole archive.

NER dependency:
  Compose requires a prior NER run per reference. Before the
  confirmation prompt, the CLI counts how many of the selected
  refs are ready (have a NER run) vs missing NER, and shows the
  split so you know what you are agreeing to.

  --ner   Runs NER first for refs missing it, then compose. Uses
          the same provider and model as compose unless
          --ner-provider / --ner-model are set.

Guardrails:
  Hive does not cap spend. Spending limits must be set and monitored
  with your service provider — hive echoes provider-reported token
  counts but never a dollar figure.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCompose(cmd, args)
	},
}

// nerRunInfo carries what compose needs about a ref's most-recent
// NER run: the run_id (for parent_run_id linking) and the raw
// output JSON (for the compose input's second section).
type nerRunInfo struct {
	RunID     string
	RawOutput string
}

func runCompose(cmd *cobra.Command, args []string) error {
	server, _ := cmd.Flags().GetString("server")
	provider, _ := cmd.Flags().GetString("provider")
	model, _ := cmd.Flags().GetString("model")
	prompt, _ := cmd.Flags().GetString("prompt")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	jsonOut, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	all, _ := cmd.Flags().GetBool("all")
	runNerFirst, _ := cmd.Flags().GetBool("ner")
	nerPrompt, _ := cmd.Flags().GetString("ner-prompt")
	nerProvider, _ := cmd.Flags().GetString("ner-provider")
	nerModel, _ := cmd.Flags().GetString("ner-model")

	server = strings.TrimRight(server, "/")
	if provider == "" {
		return fmt.Errorf("--provider is required (see `hive compose --help`)")
	}
	if prompt == "" {
		prompt = "compose_v4"
	}
	if nerPrompt == "" {
		nerPrompt = "ner_v6"
	}
	if nerProvider == "" {
		nerProvider = provider
	}
	if nerModel == "" {
		nerModel = model
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

	httpClient := &http.Client{Timeout: 30 * time.Minute}
	refs, err := resolveRefs(cmd.Context(), httpClient, server, args, all)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no references to process")
		return nil
	}

	// Pre-flight: figure out which refs already have a NER run so
	// the confirmation prompt can show the ready/missing split
	// before any tokens are spent.
	nerByRef := make(map[string]*nerRunInfo, len(refs))
	var missing []string
	if !dryRun {
		fmt.Fprintf(cmd.ErrOrStderr(), "Pre-flight: checking %d ref(s) for NER runs...\n", len(refs))
	}
	for _, id := range refs {
		info, err := fetchLatestNERRun(cmd.Context(), httpClient, server, id)
		if err != nil {
			return fmt.Errorf("pre-flight for ref %s: %w", id, err)
		}
		if info == nil {
			missing = append(missing, id)
			continue
		}
		nerByRef[id] = info
	}
	ready := len(refs) - len(missing)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Would run compose (prompt %q) against %d reference(s)\n"+
				"  Provider: %s\n  Model:    %s\n\n"+
				"  NER-ready:  %d\n"+
				"  Missing NER: %d%s\n",
			prompt, len(refs), provider, orDash(model),
			ready, len(missing),
			ifThen(runNerFirst, " (would run NER first via --ner)", " (would be skipped)"))
		for _, id := range refs {
			marker := " (has NER)"
			if _, ok := nerByRef[id]; !ok {
				marker = " (needs NER)"
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  "+id+marker)
		}
		return nil
	}

	if !yes {
		headline := fmt.Sprintf("Ready to run compose (prompt %q) over %d reference(s)\n"+
			"  NER-ready:   %d ref(s)\n"+
			"  Missing NER: %d ref(s)%s",
			prompt, len(refs), ready, len(missing),
			ifThen(runNerFirst, " (will run NER first — extra tokens)", " (will be loudly skipped)"))
		if err := confirmRun(headline, provider, model); err != nil {
			return err
		}
	}

	// If --ner: run NER for the missing refs, populating nerByRef,
	// before the compose batch. Fail-hard on any NER failure so a
	// half-populated compose batch doesn't proceed silently.
	if runNerFirst && len(missing) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\nRunning NER for %d missing ref(s) before compose...\n",
			len(missing))
		nerOpts := llmBatchOpts{
			HTTPClient:  httpClient,
			Server:      server,
			Refs:        missing,
			Provider:    nerProvider,
			Model:       nerModel,
			Prompt:      nerPrompt,
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
		if err := runBatch(cmd.Context(), nerOpts); err != nil {
			return fmt.Errorf("NER phase: %w", err)
		}
		// Re-fetch latest NER run info for the refs that just ran.
		for _, id := range missing {
			info, err := fetchLatestNERRun(cmd.Context(), httpClient, server, id)
			if err != nil {
				return fmt.Errorf("post-NER lookup for ref %s: %w", id, err)
			}
			if info == nil {
				// NER call reported success but no run row landed —
				// treat as failure so compose doesn't try to
				// proceed with a missing input.
				return fmt.Errorf("post-NER lookup: ref %s has no NER run after successful NER call", id)
			}
			nerByRef[id] = info
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "\nNER phase complete — proceeding to compose.")
	}

	// Compose phase.
	composeOpts := llmBatchOpts{
		HTTPClient:  httpClient,
		Server:      server,
		Refs:        refs,
		Provider:    provider,
		Model:       model,
		Prompt:      prompt,
		Purpose:     "compose",
		Concurrency: concurrency,
		JSONOut:     jsonOut,
		Quiet:       quiet,
		inputFn: func(ctx context.Context, refID string) (string, string, string, error) {
			info, ok := nerByRef[refID]
			if !ok {
				return "", "", "no NER run for this reference; pass --ner to run NER first", nil
			}
			jats, err := fetchAugmentedJATS(ctx, llmBatchOpts{
				HTTPClient: httpClient, Server: server,
			}, refID)
			if err != nil {
				return "", "", "", err
			}
			input := buildComposeInput(jats, info.RawOutput)
			return input, info.RunID, "", nil
		},
	}
	return runBatch(cmd.Context(), composeOpts)
}

// buildComposeInput assembles the two-section user message the
// compose prompt expects. Sections are labeled with the exact
// markdown headers documented in compose_v4.md's "Input format"
// section.
func buildComposeInput(jats, nerOutput string) string {
	var b strings.Builder
	b.WriteString("# Paper JATS body\n\n")
	b.WriteString(jats)
	b.WriteString("\n\n# NER assertions\n\n")
	b.WriteString(nerOutput)
	return b.String()
}

// fetchLatestNERRun returns the most-recent non-deleted NER run
// for the reference, or nil when none exists. Uses the existing
// GET /api/reference/{id}/llm-runs endpoint (which returns runs
// most-recent first) and filters client-side by purpose.
func fetchLatestNERRun(ctx context.Context, c *http.Client, server, refID string) (*nerRunInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		server+"/api/reference/"+refID+"/llm-runs", nil)
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list llm-runs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list llm-runs: server returned %d", resp.StatusCode)
	}
	var body struct {
		Items []struct {
			RunID     string          `json:"run_id"`
			Purpose   string          `json:"purpose"`
			RawOutput json.RawMessage `json:"raw_output"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode llm-runs: %w", err)
	}
	for _, item := range body.Items {
		if item.Purpose == "ner" {
			return &nerRunInfo{
				RunID:     item.RunID,
				RawOutput: string(item.RawOutput),
			}, nil
		}
	}
	return nil, nil
}

func ifThen(cond bool, whenTrue, whenFalse string) string {
	if cond {
		return whenTrue
	}
	return whenFalse
}

func init() {
	composeCmd.Flags().String("server", "http://127.0.0.1:2365", "base URL of the running hive server")
	composeCmd.Flags().String("provider", "", "provider name for the compose call")
	composeCmd.Flags().String("model", "", "model override for compose; empty means the provider's configured default")
	composeCmd.Flags().String("prompt", "", "override the shipped compose prompt (default: compose_v4)")
	composeCmd.Flags().Int("concurrency", 1, "number of parallel invoke calls per phase")
	composeCmd.Flags().Bool("all", false, "run against every reference in the archive")
	composeCmd.Flags().Bool("yes", false, "skip the interactive confirmation prompt (for scripting)")
	composeCmd.Flags().Bool("dry-run", false, "preview the ref list + NER-ready split; no LLM calls, no confirmation")
	composeCmd.Flags().Bool("json", false, "emit one NDJSON row per completed reference instead of the rolling progress line")
	composeCmd.Flags().Bool("quiet", false, "suppress the progress line and end-of-run summary")
	composeCmd.Flags().Bool("ner", false, "run NER first for refs missing a prior NER run (otherwise those refs are loudly skipped)")
	composeCmd.Flags().String("ner-prompt", "", "override the shipped NER prompt when --ner is set (default: ner_v6)")
	composeCmd.Flags().String("ner-provider", "", "provider for the NER phase when --ner is set (default: same as --provider)")
	composeCmd.Flags().String("ner-model", "", "model for the NER phase when --ner is set (default: same as --model)")
	rootCmd.AddCommand(composeCmd)
}
