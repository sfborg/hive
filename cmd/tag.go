// Package cmd — hive tag: bulk-invoke a shipped prompt against a
// running hive server for one, many, or all references. See the
// long description on the command below for the guardrail model
// (no --max-cost, no --max-tokens; provider spending limit is
// the sole billing safeguard).
package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

var tagCmd = &cobra.Command{
	Use:   "tag [ref-id...]",
	Short: "Bulk-invoke a prompt over one or more references",
	Long: `Runs a shipped-library prompt (ner_v6, compose_v4, ...) against every
listed reference. When --all is passed, every reference in the archive
is a candidate.

Guardrails:
  Hive does not cap spend. Spending limits must be set and monitored
  with your service provider — hive echoes provider-reported token
  counts but never a dollar figure. See the getting-started doc for
  the "set a low limit → try it → raise" first-run pattern.

Selection:
  Ref-id arguments run only those references (validated up-front).
  --all runs the whole archive.

Output:
  Progress lines print to stderr so stdout stays clean; --json emits
  one NDJSON row per completed reference containing the persisted
  run_id and provider-reported token counts (never a dollar value).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		server = strings.TrimRight(server, "/")
		if provider == "" {
			return fmt.Errorf("--provider is required (see `hive tag --help`)")
		}
		if prompt == "" {
			return fmt.Errorf("--prompt is required (e.g. ner_v6, compose_v4)")
		}
		if concurrency < 1 {
			concurrency = 1
		}

		purpose := purposeFromPrompt(prompt)
		if purpose == "" {
			return fmt.Errorf(
				"cannot infer purpose from prompt name %q — expected a name starting with ner_, compose_, or translate_",
				prompt,
			)
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
			fmt.Fprintln(os.Stderr, "no references to process")
			return nil
		}

		if dryRun {
			// Print what would run, no confirmation, no calls.
			fmt.Fprintf(os.Stdout,
				"Would run prompt %q (purpose %q) against %d reference(s)\n"+
					"  Provider: %s\n  Model:    %s\n",
				prompt, purpose, len(refs), provider, orDash(model))
			for _, id := range refs {
				fmt.Fprintln(os.Stdout, "  "+id)
			}
			return nil
		}

		if !yes {
			if err := confirmRun(prompt, provider, model, len(refs)); err != nil {
				return err
			}
		}

		return runBatch(cmd.Context(), runBatchOpts{
			HTTPClient:  httpClient,
			Server:      server,
			Refs:        refs,
			Provider:    provider,
			Model:       model,
			Prompt:      prompt,
			Purpose:     purpose,
			Concurrency: concurrency,
			JSONOut:     jsonOut,
			Quiet:       quiet,
		})
	},
}

// purposeFromPrompt maps a prompt slug to the /api/llm/invoke
// "purpose" enum. The invoke endpoint uses purpose to decide
// whether to materialize assertion rows (ner) or persist the
// run without materialization (compose, translate).
func purposeFromPrompt(name string) string {
	switch {
	case strings.HasPrefix(name, "ner_"):
		return "ner"
	case strings.HasPrefix(name, "compose_"):
		return "compose"
	case strings.HasPrefix(name, "translate_"):
		return "translate"
	}
	return ""
}

// confirmRun renders the warning + summary block and reads a
// yes/no line from stdin. Empty answers, non-yes answers, and
// unreadable stdin all count as "no" — the safe default.
func confirmRun(prompt, provider, model string, count int) error {
	fmt.Fprintf(os.Stderr,
		"Ready to run prompt %q against %d reference(s)\n"+
			"  Provider: %s\n"+
			"  Model:    %s\n\n"+
			"⚠  Warning: this will invoke LLM API calls and cost money.\n"+
			"   Spending limits must be set and monitored with your\n"+
			"   service provider (Anthropic dashboard, OpenAI usage\n"+
			"   caps, etc.). You are responsible for all incurred costs.\n\n"+
			"Continue? (yes/no): ",
		prompt, count, provider, orDash(model),
	)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("aborted (stdin unreadable): %w", err)
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans != "yes" && ans != "y" {
		return fmt.Errorf("aborted by user")
	}
	return nil
}

type runBatchOpts struct {
	HTTPClient  *http.Client
	Server      string
	Refs        []string
	Provider    string
	Model       string
	Prompt      string
	Purpose     string
	Concurrency int
	JSONOut     bool
	Quiet       bool
}

// runBatch fans the refs out across N workers, aggregates
// provider-reported token counts, and renders progress. Never
// computes a dollar figure — the summary and per-line JSON both
// carry token counts only (see project_llm_cost_surfacing.md).
func runBatch(ctx context.Context, opts runBatchOpts) error {
	total := len(opts.Refs)
	var (
		processed atomic.Int64
		failed    atomic.Int64
		inTokens  atomic.Int64
		outTokens atomic.Int64
	)
	start := time.Now()

	// Progress ticker prints to stderr on a schedule so the user
	// sees advancing counters even during a long-running provider
	// call. Guarded by --quiet and --json (JSON mode emits per-ref
	// events rather than a rolling status line).
	stop := make(chan struct{})
	if !opts.Quiet && !opts.JSONOut {
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					renderProgress(int(processed.Load()), total,
						inTokens.Load(), outTokens.Load(), start)
				}
			}
		}()
	}

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	// A per-ref channel is used only for --json output so lines
	// print in completion order (not in submission order).
	jsonCh := make(chan []byte, opts.Concurrency*2)
	jsonDone := make(chan struct{})
	if opts.JSONOut {
		go func() {
			for line := range jsonCh {
				os.Stdout.Write(line)
				os.Stdout.Write([]byte{'\n'})
			}
			close(jsonDone)
		}()
	}

	for _, id := range opts.Refs {
		wg.Add(1)
		sem <- struct{}{}
		go func(refID string) {
			defer wg.Done()
			defer func() { <-sem }()
			result, err := runOneRef(ctx, opts, refID)
			processed.Add(1)
			if err != nil {
				failed.Add(1)
				if opts.JSONOut {
					jsonCh <- mustMarshal(map[string]any{
						"reference_id": refID,
						"error":        err.Error(),
					})
				} else if !opts.Quiet {
					fmt.Fprintf(os.Stderr, "\n[ref %s] %s\n", refID[:8], err)
				}
				return
			}
			inTokens.Add(int64(result.InputTokens))
			outTokens.Add(int64(result.OutputTokens))
			if opts.JSONOut {
				jsonCh <- mustMarshal(map[string]any{
					"reference_id":            refID,
					"run_id":                  result.RunID,
					"input_tokens":            result.InputTokens,
					"output_tokens":           result.OutputTokens,
					"assertions_materialized": result.AssertionsMaterialized,
				})
			}
		}(id)
	}
	wg.Wait()
	close(stop)

	if opts.JSONOut {
		close(jsonCh)
		<-jsonDone
	}

	// Final line: overwrite the rolling status with a clean summary.
	if !opts.Quiet && !opts.JSONOut {
		renderProgress(total, total, inTokens.Load(), outTokens.Load(), start)
		fmt.Fprintln(os.Stderr)
	}

	if !opts.JSONOut {
		fmt.Fprintf(os.Stderr,
			"\nDone. %d reference(s) processed. %d failed.\n\n"+
				"Provider-reported token usage:\n"+
				"  Input:  %s tokens\n"+
				"  Output: %s tokens\n\n"+
				"Verify actual token usage on your provider's dashboard.\n",
			total-int(failed.Load()), failed.Load(),
			humanTokens(inTokens.Load()),
			humanTokens(outTokens.Load()),
		)
	}

	if failed.Load() > 0 {
		return fmt.Errorf("%d reference(s) failed", failed.Load())
	}
	return nil
}

// resolveRefs turns command-line args (explicit ref-ids or --all)
// into a validated ref-id list. Validates each explicit id by
// hitting GET /api/reference/{id}; unknown ids error out up front
// so the user doesn't hit them mid-batch.
func resolveRefs(ctx context.Context, c *http.Client, server string, args []string, all bool) ([]string, error) {
	if all {
		return listAllRefs(ctx, c, server)
	}
	// Validate each id.
	seen := make(map[string]struct{}, len(args))
	out := make([]string, 0, len(args))
	for _, id := range args {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			server+"/api/reference/"+id, nil)
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("check ref %s: %w", id, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("ref %s not found", id)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("check ref %s: server returned %d", id, resp.StatusCode)
		}
		out = append(out, id)
	}
	return out, nil
}

// listAllRefs pages through GET /api/reference?limit=... until
// the server stops returning items. Order is whatever the server
// returns (usually created-at or alphabetical), which is fine —
// the CLI's batch semantics don't depend on ordering.
func listAllRefs(ctx context.Context, c *http.Client, server string) ([]string, error) {
	var out []string
	cursor := ""
	const pageSize = 200
	for {
		u := fmt.Sprintf("%s/api/reference?limit=%d", server, pageSize)
		if cursor != "" {
			u += "&cursor=" + cursor
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list refs: %w", err)
		}
		var body struct {
			Items      []struct{ ID string } `json:"items"`
			NextCursor string                `json:"next_cursor,omitempty"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("list refs: decode: %w", err)
		}
		resp.Body.Close()
		for _, item := range body.Items {
			out = append(out, item.ID)
		}
		if body.NextCursor == "" || len(body.Items) < pageSize {
			break
		}
		cursor = body.NextCursor
	}
	return out, nil
}

type refDetail struct {
	AttachedFiles []struct {
		SHA512 string `json:"sha512"`
		Ext    string `json:"ext"`
	} `json:"attached_files"`
}

type runResult struct {
	RunID                  string
	InputTokens            int
	OutputTokens           int
	AssertionsMaterialized int
}

// runOneRef fetches the primary augmented JATS for a reference,
// POSTs it to /api/llm/invoke, and returns the persisted run id
// plus provider-reported token counts.
//
// Format-preference for the primary augmented JATS: xml > jats >
// pdf > epub. Matches the reader's preferred-format rule so the
// tagging pass runs against whatever the reader would show.
func runOneRef(ctx context.Context, opts runBatchOpts, refID string) (*runResult, error) {
	// Load ref detail to pick the primary sha.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		opts.Server+"/api/reference/"+refID, nil)
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch ref: %w", err)
	}
	var detail refDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("decode ref: %w", err)
	}
	resp.Body.Close()
	if len(detail.AttachedFiles) == 0 {
		return nil, errors.New("no attached files")
	}
	primary := pickPrimaryFormat(detail.AttachedFiles)
	if primary.SHA512 == "" {
		return nil, errors.New("no attached format supports augmented JATS")
	}

	// Fetch the augmented JATS from the sidecar library.
	jatsReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		opts.Server+"/api/reference/library/"+primary.SHA512+"/taxpub", nil)
	jatsResp, err := opts.HTTPClient.Do(jatsReq)
	if err != nil {
		return nil, fmt.Errorf("fetch augmented JATS: %w", err)
	}
	defer jatsResp.Body.Close()
	if jatsResp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no augmented JATS for sha %s — run annotation first",
			primary.SHA512[:12])
	}
	if jatsResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch augmented JATS: server returned %d",
			jatsResp.StatusCode)
	}
	jats, err := io.ReadAll(jatsResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read augmented JATS: %w", err)
	}

	// POST the invoke request.
	invokeBody := map[string]any{
		"provider":     opts.Provider,
		"prompt":       opts.Prompt,
		"input":        string(jats),
		"purpose":      opts.Purpose,
		"reference_id": refID,
	}
	if opts.Model != "" {
		invokeBody["model"] = opts.Model
	}
	buf, _ := json.Marshal(invokeBody)
	post, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		opts.Server+"/api/llm/invoke", bytes.NewReader(buf))
	post.Header.Set("Content-Type", "application/json")
	postResp, err := opts.HTTPClient.Do(post)
	if err != nil {
		return nil, fmt.Errorf("invoke: %w", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode >= 400 {
		msg, _ := io.ReadAll(postResp.Body)
		return nil, fmt.Errorf("invoke: server returned %d: %s",
			postResp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var invokeResp struct {
		RunID    string `json:"run_id"`
		Envelope struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"envelope"`
		AssertionsMaterialized int `json:"assertions_materialized"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&invokeResp); err != nil {
		return nil, fmt.Errorf("decode invoke response: %w", err)
	}
	return &runResult{
		RunID:                  invokeResp.RunID,
		InputTokens:            invokeResp.Envelope.InputTokens,
		OutputTokens:           invokeResp.Envelope.OutputTokens,
		AssertionsMaterialized: invokeResp.AssertionsMaterialized,
	}, nil
}

// pickPrimaryFormat returns the highest-priority format from the
// attached_files list. Publisher XML/JATS wins because it carries
// the richest native structure; PDF (via GROBID → jatsconv) is
// the next-best; EPUB (via pandoc) rounds out the set. Formats
// without a known augmented-JATS path return an empty result and
// caller reports "no augmented JATS available".
func pickPrimaryFormat(files []struct {
	SHA512 string `json:"sha512"`
	Ext    string `json:"ext"`
}) struct {
	SHA512 string `json:"sha512"`
	Ext    string `json:"ext"`
} {
	rank := func(ext string) int {
		switch ext {
		case "xml", "jats":
			return 0
		case "pdf":
			return 1
		case "epub":
			return 2
		}
		return 99
	}
	best := -1
	for i, f := range files {
		if rank(f.Ext) == 99 {
			continue
		}
		if best < 0 || rank(f.Ext) < rank(files[best].Ext) {
			best = i
		}
	}
	if best < 0 {
		return struct {
			SHA512 string `json:"sha512"`
			Ext    string `json:"ext"`
		}{}
	}
	return files[best]
}

// renderProgress prints a rolling status line to stderr. Uses \r
// to overwrite the previous line so the terminal stays clean; a
// \n after the run flushes it to a final line.
func renderProgress(processed, total int, tin, tout int64, start time.Time) {
	elapsed := time.Since(start).Round(time.Second)
	fmt.Fprintf(os.Stderr, "\r[ %4d / %4d ]  in: %s  out: %s  elapsed %s   ",
		processed, total, humanTokens(tin), humanTokens(tout), elapsed)
}

// humanTokens formats an int64 token count as a compact
// human-friendly string. 1,234,567 → "1.23M". Zero returns "0".
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func orDash(s string) string {
	if s == "" {
		return "(provider default)"
	}
	return s
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// This shouldn't happen for map[string]any built from
		// primitives; fall back to a sentinel line so the loop
		// doesn't die.
		return []byte(fmt.Sprintf(`{"error":"marshal failed: %s"}`, err.Error()))
	}
	return b
}

func init() {
	tagCmd.Flags().String("server", "http://127.0.0.1:2365", "base URL of the running hive server")
	tagCmd.Flags().String("provider", "", "provider name (matches an entry in your llm_providers config)")
	tagCmd.Flags().String("model", "", "model override; empty means the provider's configured default")
	tagCmd.Flags().String("prompt", "", "shipped prompt name (e.g. ner_v6, compose_v4)")
	tagCmd.Flags().Int("concurrency", 1, "number of parallel invoke calls; default 1 avoids accidentally slamming the provider")
	tagCmd.Flags().Bool("all", false, "run against every reference in the archive")
	tagCmd.Flags().Bool("yes", false, "skip the interactive confirmation prompt (for scripting)")
	tagCmd.Flags().Bool("dry-run", false, "preview the ref list, provider, and model; no LLM calls, no confirmation")
	tagCmd.Flags().Bool("json", false, "emit one NDJSON row per completed reference instead of the rolling progress line")
	tagCmd.Flags().Bool("quiet", false, "suppress the progress line and end-of-run summary")
	rootCmd.AddCommand(tagCmd)
}
