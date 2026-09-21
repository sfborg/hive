// Package cmd — shared batch-invoke plumbing for hive ner and
// hive compose. Both commands walk a ref list, POST to
// /api/llm/invoke per ref, aggregate provider-reported token
// counts, and render progress. Only the input assembly and the
// prompt name differ; everything else lives here.
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// llmBatchOpts drives runBatch. inputFn is what each subcommand
// customizes — it produces the (input, parent_run_id, skip
// reason) tuple for one ref. When skipReason is non-empty the
// ref is loudly skipped rather than sent to the provider; that's
// the guardrail for the "compose on a ref with no NER" case.
type llmBatchOpts struct {
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

	// inputFn returns (input, parent_run_id, skip_reason). When
	// skip_reason is non-empty, the ref is reported as skipped
	// (with the reason) and no invoke call is made.
	inputFn func(ctx context.Context, refID string) (string, string, string, error)
}

type runResult struct {
	RunID                  string
	InputTokens            int
	OutputTokens           int
	AssertionsMaterialized int
}

// problemProviderAccountLimit is the problem type the server returns
// from /api/llm/invoke when the LLM provider refuses a request on
// account grounds: spend limit reached, credits exhausted or API key
// rejected.
const problemProviderAccountLimit = "urn:hive:error:provider-account-limit"

// maxConsecutiveInvokeFailures stops a batch after this many invoke
// failures in a row. It covers providers whose limit errors the
// server can't classify: once a provider starts refusing every
// request, the remaining references would fail the same way.
const maxConsecutiveInvokeFailures = 5

// providerLimitError is an invoke failure of type
// problemProviderAccountLimit.
type providerLimitError struct {
	title  string
	detail string
}

func (e *providerLimitError) Error() string {
	return "invoke: " + e.title + ": " + e.detail
}

// runBatch fans the refs out across N workers, aggregates
// provider-reported token counts, and renders progress. Never
// computes a dollar figure — the summary and per-line JSON both
// carry token counts only (see project_llm_cost_surfacing.md).
func runBatch(ctx context.Context, opts llmBatchOpts) error {
	total := len(opts.Refs)
	var (
		processed atomic.Int64
		succeeded atomic.Int64
		failed    atomic.Int64
		skipped   atomic.Int64
		inTokens  atomic.Int64
		outTokens atomic.Int64
	)
	start := time.Now()

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

	// Dispatch stops after an account-level refusal from the provider
	// or maxConsecutiveInvokeFailures failed invokes in a row: every
	// later request would be refused the same way. Calls already in
	// flight run to completion; the provider has accepted them.
	var (
		halted      atomic.Bool
		haltOnce    sync.Once
		haltErr     error
		consecutive atomic.Int64
	)
	halt := func(err error) {
		haltOnce.Do(func() {
			haltErr = err
			halted.Store(true)
		})
	}

	var notAttempted []string
	for i, id := range opts.Refs {
		sem <- struct{}{}
		if halted.Load() {
			<-sem
			notAttempted = opts.Refs[i:]
			break
		}
		wg.Add(1)
		go func(refID string) {
			defer wg.Done()
			defer func() { <-sem }()

			input, parentRunID, skipReason, err := opts.inputFn(ctx, refID)
			if err != nil {
				processed.Add(1)
				failed.Add(1)
				emitFailure(opts, jsonCh, refID, err)
				return
			}
			if skipReason != "" {
				processed.Add(1)
				skipped.Add(1)
				emitSkip(opts, jsonCh, refID, skipReason)
				return
			}
			result, err := invokeLLM(ctx, opts, refID, input, parentRunID)
			processed.Add(1)
			if err != nil {
				failed.Add(1)
				emitFailure(opts, jsonCh, refID, err)
				var limit *providerLimitError
				if errors.As(err, &limit) {
					halt(err)
				} else if consecutive.Add(1) >= maxConsecutiveInvokeFailures {
					halt(fmt.Errorf("%d consecutive requests failed; last error: %w",
						maxConsecutiveInvokeFailures, err))
				}
				return
			}
			consecutive.Store(0)
			succeeded.Add(1)
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
		for _, id := range notAttempted {
			jsonCh <- mustMarshal(map[string]any{
				"reference_id":  id,
				"not_attempted": "run stopped",
			})
		}
		close(jsonCh)
		<-jsonDone
	}

	if !opts.Quiet && !opts.JSONOut {
		renderProgress(int(processed.Load()), total, inTokens.Load(), outTokens.Load(), start)
		fmt.Fprintln(os.Stderr)
	}

	if !opts.JSONOut {
		if halted.Load() {
			fmt.Fprintf(os.Stderr,
				"\nStopped early; %d reference(s) not attempted:\n  %s\n",
				len(notAttempted), haltErr)
		}
		fmt.Fprintf(os.Stderr,
			"\nDone. %d succeeded, %d skipped, %d failed (of %d total).\n\n"+
				"Provider-reported token usage:\n"+
				"  Input:  %s tokens\n"+
				"  Output: %s tokens\n\n"+
				"Verify actual token usage on your provider's dashboard.\n",
			succeeded.Load(), skipped.Load(), failed.Load(), total,
			humanTokens(inTokens.Load()),
			humanTokens(outTokens.Load()),
		)
	}

	if halted.Load() {
		return fmt.Errorf("stopped early, %d reference(s) not attempted: %w",
			len(notAttempted), haltErr)
	}
	if failed.Load() > 0 {
		return fmt.Errorf("%d reference(s) failed", failed.Load())
	}
	return nil
}

func emitFailure(opts llmBatchOpts, jsonCh chan<- []byte, refID string, err error) {
	if opts.JSONOut {
		jsonCh <- mustMarshal(map[string]any{
			"reference_id": refID,
			"error":        err.Error(),
		})
	} else if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "\n[ref %s] error: %s\n", refID[:8], err)
	}
}

func emitSkip(opts llmBatchOpts, jsonCh chan<- []byte, refID, reason string) {
	if opts.JSONOut {
		jsonCh <- mustMarshal(map[string]any{
			"reference_id": refID,
			"skipped":      reason,
		})
	} else if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "\n[ref %s] skipped: %s\n", refID[:8], reason)
	}
}

// invokeLLM POSTs an invoke request and returns the run + token
// counts. Reused by both ner and compose commands.
func invokeLLM(ctx context.Context, opts llmBatchOpts, refID, input, parentRunID string) (*runResult, error) {
	body := map[string]any{
		"provider":     opts.Provider,
		"prompt":       opts.Prompt,
		"input":        input,
		"purpose":      opts.Purpose,
		"reference_id": refID,
	}
	if opts.Model != "" {
		body["model"] = opts.Model
	}
	if parentRunID != "" {
		body["parent_run_id"] = parentRunID
	}
	buf, _ := json.Marshal(body)
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
		var p struct {
			Type   string `json:"type"`
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(msg, &p) == nil && p.Type == problemProviderAccountLimit {
			return nil, &providerLimitError{title: p.Title, detail: p.Detail}
		}
		return nil, fmt.Errorf("invoke: server returned %d: %s",
			postResp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var resp struct {
		RunID    string `json:"run_id"`
		Envelope struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"envelope"`
		AssertionsMaterialized int `json:"assertions_materialized"`
	}
	if err := json.NewDecoder(postResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode invoke response: %w", err)
	}
	return &runResult{
		RunID:                  resp.RunID,
		InputTokens:            resp.Envelope.InputTokens,
		OutputTokens:           resp.Envelope.OutputTokens,
		AssertionsMaterialized: resp.AssertionsMaterialized,
	}, nil
}

// confirmRun renders a caller-supplied headline block plus the
// standard warning, then reads a yes/no line from stdin. Empty
// answers, non-yes answers, and unreadable stdin all count as
// "no" — the safe default. Warning wording matches
// project_llm_cost_surfacing memory.
//
// Callers own the full headline (including ref counts, phase
// breakdown, etc.) so compose can show its ready/missing split
// without the headline template turning it into a run-on line.
func confirmRun(headline, provider, model string) error {
	fmt.Fprintf(os.Stderr,
		"%s\n"+
			"  Provider: %s\n"+
			"  Model:    %s\n\n"+
			"⚠  Warning: this will invoke LLM API calls and cost money.\n"+
			"   You are responsible for all incurred costs.\n\n"+
			"   It is strongly recommended that you set spending limits with\n"+
			"   your LLM service provider and vigilantly monitor your spending.\n\n"+
			"   You can terminate the process with Ctrl+C at any time.\n\n"+
			"Continue? (yes/no): ",
		headline, provider, orDash(model),
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

// resolveRefs turns command-line args (explicit ref-ids or --all)
// into a validated ref-id list. Validates each explicit id by
// hitting GET /api/reference/{id}; unknown ids error out up front
// so the user doesn't hit them mid-batch.
func resolveRefs(ctx context.Context, c *http.Client, server string, args []string, all bool) ([]string, error) {
	if all {
		return listAllRefs(ctx, c, server)
	}
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

// attachedFile / refDetail — shared struct types used by both
// commands' input assembly.
type attachedFile struct {
	SHA512 string `json:"sha512"`
	Ext    string `json:"ext"`
}

type refDetail struct {
	AttachedFiles []attachedFile `json:"attached_files"`
}

// fetchAugmentedJATS returns the annotated JATS body for the
// reference's primary format sha. Preference order: publisher
// XML/JATS > PDF-derived > EPUB-derived.
func fetchAugmentedJATS(ctx context.Context, opts llmBatchOpts, refID string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		opts.Server+"/api/reference/"+refID, nil)
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch ref: %w", err)
	}
	var detail refDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		resp.Body.Close()
		return "", fmt.Errorf("decode ref: %w", err)
	}
	resp.Body.Close()
	if len(detail.AttachedFiles) == 0 {
		return "", errors.New("no attached files")
	}
	primary := pickPrimaryFormat(detail.AttachedFiles)
	if primary.SHA512 == "" {
		return "", errors.New("no attached format supports augmented JATS")
	}
	jatsReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		opts.Server+"/api/reference/library/"+primary.SHA512+"/taxpub", nil)
	jatsResp, err := opts.HTTPClient.Do(jatsReq)
	if err != nil {
		return "", fmt.Errorf("fetch augmented JATS: %w", err)
	}
	defer jatsResp.Body.Close()
	if jatsResp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("no augmented JATS for sha %s — run annotation first",
			primary.SHA512[:12])
	}
	if jatsResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch augmented JATS: server returned %d",
			jatsResp.StatusCode)
	}
	jats, err := io.ReadAll(jatsResp.Body)
	if err != nil {
		return "", fmt.Errorf("read augmented JATS: %w", err)
	}
	return string(jats), nil
}

func pickPrimaryFormat(files []attachedFile) attachedFile {
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
		return attachedFile{}
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
		return []byte(fmt.Sprintf(`{"error":"marshal failed: %s"}`, err.Error()))
	}
	return b
}

// latestPromptForPurpose queries the server's prompt catalog and
// returns the highest-versioned prompt name for the given purpose
// ("ner", "compose", "translate"). Version is parsed from a
// strict `_v<int>` suffix at the end of the name — names without
// that shape are ignored. When no match exists, returns an error
// so callers can fail loud rather than silently using nothing.
//
// The server is the source of truth for what prompts exist —
// both shipped/embedded prompts and any DB-added ones show up
// in /api/prompt. Auto-picking the latest means adding
// ner_v9.md to pkg/prompts and rebuilding the container is
// enough for hive ner (no --prompt flag) to pick it up.
func latestPromptForPurpose(ctx context.Context, c *http.Client, server, purpose string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/prompt", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("list prompts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list prompts: server returned %d", resp.StatusCode)
	}
	var body struct {
		Items []struct {
			Name    string `json:"name"`
			Purpose string `json:"purpose"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode prompts: %w", err)
	}
	bestName := ""
	bestVersion := -1
	for _, item := range body.Items {
		if item.Purpose != purpose {
			continue
		}
		// Strict `_v<int>` suffix. Everything before the `_v` is
		// the base name (e.g., "ner"). Only unqualified integer
		// versions win — a hypothetical `ner_v9_experimental`
		// stays off the auto-latest track.
		idx := strings.LastIndex(item.Name, "_v")
		if idx < 0 {
			continue
		}
		v, err := strconv.Atoi(item.Name[idx+2:])
		if err != nil {
			continue
		}
		if v > bestVersion {
			bestVersion = v
			bestName = item.Name
		}
	}
	if bestName == "" {
		return "", fmt.Errorf("no versioned prompt found for purpose %q", purpose)
	}
	return bestName, nil
}
