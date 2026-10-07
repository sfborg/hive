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

	"github.com/sfborg/hive/pkg/llm"
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
// NER run: the run_id (for parent_run_id linking), the raw output
// JSON (for the compose input's second section), and the NER
// call's reported output token count (used to predict whether
// compose would fit under the compose model's output cap).
type nerRunInfo struct {
	RunID           string
	RawOutput       string
	NEROutputTokens int
}

// composeOutputRatio scales the NER call's output-token count to
// a compose-output prediction. Compose emits a coherent tree
// structure that consolidates NER assertions with added
// relationships and per-field provenance — usually the compose
// output is similar in size to the NER output, but larger papers
// can generate more relationship-graph overhead than NER's flat
// assertion list. A ratio of 1.3 gives ~30% headroom for that
// structural inflation while flagging the papers that are
// genuinely at risk of truncation.
const composeOutputRatio = 1.3

func runCompose(cmd *cobra.Command, args []string) error {
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
	runNerFirst, _ := cmd.Flags().GetBool("ner")
	nerPrompt, _ := cmd.Flags().GetString("ner-prompt")
	nerProvider, _ := cmd.Flags().GetString("ner-provider")
	nerModel, _ := cmd.Flags().GetString("ner-model")
	nerEffort, _ := cmd.Flags().GetString("ner-effort")

	server = strings.TrimRight(server, "/")
	if provider == "" {
		return fmt.Errorf("--provider is required (see `hive compose --help`)")
	}
	if !llm.ValidEffort(effort) || !llm.ValidEffort(nerEffort) {
		return fmt.Errorf("--effort and --ner-effort must be one of %s", strings.Join(llm.Efforts, ", "))
	}
	httpClient := &http.Client{Timeout: 30 * time.Minute}
	if prompt == "" {
		latest, err := latestPromptForPurpose(cmd.Context(), httpClient, server, "compose")
		if err != nil {
			return fmt.Errorf("resolve latest compose prompt: %w", err)
		}
		prompt = latest
	}
	if nerPrompt == "" {
		latest, err := latestPromptForPurpose(cmd.Context(), httpClient, server, "ner")
		if err != nil {
			return fmt.Errorf("resolve latest ner prompt: %w", err)
		}
		nerPrompt = latest
	}
	if nerProvider == "" {
		nerProvider = provider
	}
	if nerModel == "" {
		nerModel = model
	}
	if nerEffort == "" {
		nerEffort = effort
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

	// Size-based split: predict compose output for each NER-ready
	// ref as (NER output tokens * composeOutputRatio) and compare
	// against the compose model's declared output cap. Refs whose
	// predicted output would exceed the cap are marked "too big"
	// and skipped rather than burning tokens on a truncated tree.
	// When the compose model is not in the limits table
	// (composeModelCap returns 0), the guardrail is disabled and
	// every NER-ready ref is treated as fitting.
	cap := composeModelCap(model)
	var tooBig []string
	if cap > 0 {
		for id, info := range nerByRef {
			predicted := int(float64(info.NEROutputTokens) * composeOutputRatio)
			if predicted > cap {
				tooBig = append(tooBig, id)
			}
		}
	}
	tooBigSet := make(map[string]struct{}, len(tooBig))
	for _, id := range tooBig {
		tooBigSet[id] = struct{}{}
	}
	fitCount := len(nerByRef) - len(tooBig)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Would run compose (prompt %q) against %d reference(s)\n"+
				"  Provider: %s\n  Model:    %s\n\n"+
				"  NER-ready + fit for model: %d\n"+
				"  NER-ready but too big for model: %d\n"+
				"  Missing NER: %d%s\n",
			prompt, len(refs), provider, modelLabel(model, effort),
			fitCount, len(tooBig), len(missing),
			ifThen(runNerFirst, " (would run NER first via --ner)", " (would be skipped)"))
		for _, id := range refs {
			var marker string
			if info, ok := nerByRef[id]; ok {
				predicted := int(float64(info.NEROutputTokens) * composeOutputRatio)
				if _, big := tooBigSet[id]; big {
					marker = fmt.Sprintf(" (has NER — TOO BIG for model: NER emitted %d output tokens; predicted compose ~%d > %d cap)",
						info.NEROutputTokens, predicted, cap)
				} else {
					marker = fmt.Sprintf(" (has NER, fits: NER emitted %d output tokens; predicted compose ~%d ≤ %d cap)",
						info.NEROutputTokens, predicted, cap)
				}
			} else {
				marker = " (needs NER)"
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  "+id+marker)
		}
		if cap == 0 {
			fmt.Fprintln(cmd.OutOrStdout(),
				"\nNote: compose model has no entry in the limits table; size-based skipping is disabled.")
		}
		return nil
	}

	if !yes {
		tooBigNote := ""
		if len(tooBig) > 0 {
			tooBigNote = fmt.Sprintf("\n  Too big for model:   %d ref(s)  (skipped — pick a bigger-output model to include these)", len(tooBig))
		}
		headline := fmt.Sprintf("Ready to run compose (prompt %q) over %d reference(s)\n"+
			"  NER-ready + fit:    %d ref(s)\n"+
			"  Missing NER:        %d ref(s)%s%s",
			prompt, len(refs), fitCount, len(missing),
			ifThen(runNerFirst, " (will run NER first — extra tokens)", " (will be loudly skipped)"),
			tooBigNote)
		if err := confirmRun(headline, provider, modelLabel(model, effort)); err != nil {
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
			Effort:      nerEffort,
			Thinking:    thinking,
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
		Effort:      effort,
		Thinking:    thinking,
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
			if _, big := tooBigSet[refID]; big {
				return "", "", fmt.Sprintf(
					"NER output was %d tokens; predicted compose output (%d) exceeds model cap (%d) — pick a bigger-output model to include this ref",
					info.NEROutputTokens,
					int(float64(info.NEROutputTokens)*composeOutputRatio),
					cap,
				), nil
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
			Envelope  struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"envelope"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode llm-runs: %w", err)
	}
	for _, item := range body.Items {
		if item.Purpose == "ner" {
			return &nerRunInfo{
				RunID:           item.RunID,
				RawOutput:       string(item.RawOutput),
				NEROutputTokens: item.Envelope.OutputTokens,
			}, nil
		}
	}
	return nil, nil
}

// composeModelCap returns the compose model's declared output cap
// (from the shared limits table). Returns 0 when the model isn't
// in the table — in that case, size-based skipping is disabled
// and the batch proceeds without the guardrail. Callers should
// prefer picking a model that IS in the limits table so the
// guardrail is active.
func composeModelCap(model string) int {
	if model == "" {
		return 0
	}
	lim, known := llm.LookupLimits(model)
	if !known {
		return 0
	}
	return lim.MaxOutputTokens
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
	composeCmd.Flags().Bool("thinking-summary", false, "request a summary of the model's reasoning, stored on each run (Anthropic models that think by default only; no extra cost)")
	composeCmd.Flags().String("effort", "", "reasoning effort: low | medium | high | xhigh | max (Anthropic providers only); empty means the model default")
	composeCmd.Flags().String("prompt", "", "shipped compose prompt name (default: highest-versioned compose_v* prompt on the server)")
	composeCmd.Flags().Int("concurrency", 1, "number of parallel invoke calls per phase")
	composeCmd.Flags().Bool("all", false, "run against every reference in the archive")
	composeCmd.Flags().Bool("yes", false, "skip the interactive confirmation prompt (for scripting)")
	composeCmd.Flags().Bool("dry-run", false, "preview the ref list + NER-ready split; no LLM calls, no confirmation")
	composeCmd.Flags().Bool("json", false, "emit one NDJSON row per completed reference instead of the rolling progress line")
	composeCmd.Flags().Bool("quiet", false, "suppress the progress line and end-of-run summary")
	composeCmd.Flags().Bool("ner", false, "run NER first for refs missing a prior NER run (otherwise those refs are loudly skipped)")
	composeCmd.Flags().String("ner-prompt", "", "shipped NER prompt name when --ner is set (default: highest-versioned ner_v* prompt on the server)")
	composeCmd.Flags().String("ner-provider", "", "provider for the NER phase when --ner is set (default: same as --provider)")
	composeCmd.Flags().String("ner-model", "", "model for the NER phase when --ner is set (default: same as --model)")
	composeCmd.Flags().String("ner-effort", "", "reasoning effort for the NER phase when --ner is set (default: same as --effort)")
	rootCmd.AddCommand(composeCmd)
}
