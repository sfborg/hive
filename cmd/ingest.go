package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var ingestCmd = &cobra.Command{
	Use:           "ingest <file>...",
	Short:         "Bulk-ingest paper files into a running hive server",
	Long: `Bulk-ingest paper files (PDF / JATS XML / EPUB / HTML) into a running
hive server.

Files are grouped by basename: 'paper.pdf' and 'paper.jats.xml' upload as
attachments to the same reference. DOI extraction (via GROBID for PDFs, via
JATS metadata for XML) drives cross-group deduplication too — dropping a
paper's JATS after its PDF is already ingested attaches the JATS to the
existing reference rather than minting a duplicate.

The hive server is expected to be up. Point --server at its base URL
(default http://127.0.0.1:2365). The CLI does no direct archive access;
everything routes through the HTTP API so the running server's state
machine stays authoritative.

The --license flag records a verified OA license on each reference the
ingest touches. Skip it when you want to attach files first and set the
license through the WUI later.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		server, _ := cmd.Flags().GetString("server")
		license, _ := cmd.Flags().GetString("license")
		licenseSource, _ := cmd.Flags().GetString("license-source")
		quiet, _ := cmd.Flags().GetBool("quiet")

		server = strings.TrimRight(server, "/")
		groups := groupIngestFiles(args)

		client := &http.Client{Timeout: 5 * time.Minute}
		summary := &ingestSummary{}

		for _, g := range groups {
			refID, err := runIngestGroup(client, server, g, license, licenseSource, summary, quiet)
			if err != nil {
				fmt.Fprintf(os.Stderr, "group %s: %v\n", g.base, err)
				summary.groupsFailed++
				continue
			}
			_ = refID
			summary.groupsIngested++
		}

		if !quiet {
			fmt.Fprintln(os.Stdout)
			fmt.Fprintf(os.Stdout, "groups ingested: %d\n", summary.groupsIngested)
			fmt.Fprintf(os.Stdout, "groups failed:   %d\n", summary.groupsFailed)
			fmt.Fprintf(os.Stdout, "files uploaded:  %d\n", summary.filesUploaded)
			fmt.Fprintf(os.Stdout, "references created: %d\n", summary.referencesCreated)
			fmt.Fprintf(os.Stdout, "references attached-to: %d\n", summary.referencesAttached)
			fmt.Fprintf(os.Stdout, "licenses set:    %d\n", summary.licensesSet)
		}
		if summary.groupsFailed > 0 {
			return fmt.Errorf("%d group(s) failed", summary.groupsFailed)
		}
		return nil
	},
}

type ingestSummary struct {
	groupsIngested     int
	groupsFailed       int
	filesUploaded      int
	referencesCreated  int
	referencesAttached int
	licensesSet        int
}

// ingestGroup is one paper's files (same basename, different
// extensions). Uploads the "primary" file first (JATS > PDF >
// EPUB > HTML — the ordering favors formats with the most
// structured metadata for the reference row), then attaches
// each remaining sha to the resulting reference id.
type ingestGroup struct {
	base  string
	files []string // ordered by uploadOrder(ext)
}

func groupIngestFiles(paths []string) []ingestGroup {
	// Group by basename with extension stripped. Compound
	// extensions like "jats.xml" are handled by stripping the
	// final .xml first and then noticing the .jats suffix.
	byBase := make(map[string][]string)
	for _, p := range paths {
		base := ingestBasename(p)
		byBase[base] = append(byBase[base], p)
	}
	out := make([]ingestGroup, 0, len(byBase))
	for base, files := range byBase {
		sort.Slice(files, func(i, j int) bool {
			return uploadOrder(files[i]) < uploadOrder(files[j])
		})
		out = append(out, ingestGroup{base: base, files: files})
	}
	// Stable output order for deterministic run summaries.
	sort.Slice(out, func(i, j int) bool { return out[i].base < out[j].base })
	return out
}

// ingestBasename returns the filename with its extension(s)
// stripped so multi-format uploads for one paper collapse to a
// shared group key. Compound extensions:
//   "foo.jats.xml" → "foo"
//   "foo.tei.xml"  → "foo"
//   "foo.grobid.xml" → "foo"
// Single extensions:
//   "foo.pdf" → "foo"
//   "foo.epub" → "foo"
//   "foo.html" → "foo"
func ingestBasename(p string) string {
	name := filepath.Base(p)
	// Strip up to two extension segments, but only when the
	// inner segment is a known qualifier (jats / tei / grobid).
	// Otherwise a paper titled "Notulae Algarum No. 47" would
	// lose real title content.
	ext := filepath.Ext(name)
	trimmed := strings.TrimSuffix(name, ext)
	inner := filepath.Ext(trimmed)
	if inner == ".jats" || inner == ".tei" || inner == ".grobid" {
		return strings.TrimSuffix(trimmed, inner)
	}
	return trimmed
}

// uploadOrder ranks formats for the primary-upload choice.
// JATS wins because it carries the richest structured metadata
// (title, authors, DOI, publisher) so the server-side reference
// row lands with the best-quality metadata. PDF comes next
// (GROBID extracts what it can). EPUB and HTML round out the
// order.
func uploadOrder(path string) int {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".jats.xml"),
		strings.HasSuffix(name, ".jats"):
		return 0
	case strings.HasSuffix(name, ".xml"):
		return 1
	case strings.HasSuffix(name, ".pdf"):
		return 2
	case strings.HasSuffix(name, ".epub"):
		return 3
	case strings.HasSuffix(name, ".html"),
		strings.HasSuffix(name, ".htm"):
		return 4
	}
	return 9
}

// runIngestGroup uploads the primary file, then attaches each
// remaining file to the resulting reference. Returns the
// reference id that the group ended up owning.
func runIngestGroup(
	client *http.Client, server string, g ingestGroup,
	license, licenseSource string, summary *ingestSummary, quiet bool,
) (string, error) {
	if len(g.files) == 0 {
		return "", nil
	}
	primary := g.files[0]
	res, err := uploadFile(client, server, primary)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", primary, err)
	}
	summary.filesUploaded++

	// Decide the reference id: prefer an existing match by sha
	// (curator uploading again over a prior attachment), then by
	// DOI (multi-format case), else the newly-created row.
	var refID string
	var action string
	switch {
	case len(res.AttachedReferences) > 0:
		refID = res.AttachedReferences[0].ID
		action = "reused (sha match)"
		summary.referencesAttached++
	case len(res.MatchedByDOI) > 0:
		refID = res.MatchedByDOI[0].ID
		action = "attached to existing (DOI match)"
		summary.referencesAttached++
		// The upload flow returns a matched-by-DOI candidate but
		// doesn't attach automatically; we do it here so the WUI
		// contract of "client decides" stays intact.
		if err := attachSha(client, server, refID, res.SHA512); err != nil {
			return "", fmt.Errorf("attach %s to %s: %w", res.SHA512[:12], refID, err)
		}
	default:
		refID, err = createReferenceFromUpload(client, server, res)
		if err != nil {
			return "", fmt.Errorf("create reference from %s: %w", primary, err)
		}
		action = "created"
		summary.referencesCreated++
	}

	if !quiet {
		title := ""
		if res.Metadata != nil {
			title = res.Metadata.Title
		}
		if title == "" && len(res.AttachedReferences) > 0 {
			title = res.AttachedReferences[0].Title
		}
		if title == "" && len(res.MatchedByDOI) > 0 {
			title = res.MatchedByDOI[0].Title
		}
		if title == "" {
			title = g.base
		}
		fmt.Fprintf(os.Stdout, "%s\n  → %s (%s, doi=%s)\n",
			title, refID, action, orEmpty(res.DOI))
	}

	// Attach each remaining format file to the same reference.
	for _, f := range g.files[1:] {
		up, err := uploadFile(client, server, f)
		if err != nil {
			return "", fmt.Errorf("upload %s: %w", f, err)
		}
		summary.filesUploaded++
		if err := attachSha(client, server, refID, up.SHA512); err != nil {
			return "", fmt.Errorf("attach %s to %s: %w", up.SHA512[:12], refID, err)
		}
		if !quiet {
			fmt.Fprintf(os.Stdout, "  + attached %s (%s)\n",
				filepath.Base(f), up.SHA512[:12])
		}
	}

	// Set license on the reference row when requested.
	if license != "" {
		if err := setLicense(client, server, refID, license, licenseSource); err != nil {
			return "", fmt.Errorf("set license on %s: %w", refID, err)
		}
		summary.licensesSet++
		if !quiet {
			fmt.Fprintf(os.Stdout, "  · license=%s\n", license)
		}
	}
	return refID, nil
}

// uploadedFile mirrors the server's apiReferenceUpload response
// with only the fields the CLI consumes.
type uploadedFile struct {
	SHA512             string                `json:"sha512"`
	Ext                string                `json:"ext"`
	Size               int64                 `json:"size"`
	AlreadyExisted     bool                  `json:"already_existed"`
	DOI                string                `json:"doi"`
	Metadata           *uploadedMetadata     `json:"metadata,omitempty"`
	AttachedReferences []attachedRefSummary  `json:"attached_references,omitempty"`
	MatchedByDOI       []attachedRefSummary  `json:"matched_by_doi,omitempty"`
}

type uploadedMetadata struct {
	Title          string `json:"title"`
	Author         string `json:"author"`
	Issued         string `json:"issued"`
	ContainerTitle string `json:"container_title"`
	Volume         string `json:"volume"`
	Issue          string `json:"issue"`
	Page           string `json:"page"`
	Publisher      string `json:"publisher"`
}

type attachedRefSummary struct {
	ID       string `json:"id"`
	Citation string `json:"citation"`
	Title    string `json:"title"`
}

func uploadFile(client *http.Client, server, path string) (*uploadedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST",
		server+"/api/reference/upload", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out uploadedFile
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func attachSha(client *http.Client, server, refID, sha string) error {
	body := bytes.NewBufferString(fmt.Sprintf(`{"sha512":%q}`, sha))
	req, err := http.NewRequest("POST",
		fmt.Sprintf("%s/api/reference/%s/attach-pdf", server, refID), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// createReferenceFromUpload builds a minimal reference row from
// the upload response's extracted metadata + attaches the sha.
// TODO(follow-up): fold this into POST /api/reference/upload
// with a ?create=true option so the round-trip is a single call.
func createReferenceFromUpload(
	client *http.Client, server string, up *uploadedFile,
) (string, error) {
	title := ""
	author := ""
	year := ""
	if up.Metadata != nil {
		title = up.Metadata.Title
		author = up.Metadata.Author
		year = up.Metadata.Issued
	}
	if title == "" {
		title = "Untitled (bulk ingest)"
	}
	body, err := json.Marshal(map[string]any{
		"title":  title,
		"author": author,
		"issued": year,
		"doi":    up.DOI,
	})
	if err != nil {
		return "", err
	}
	// POST /api/reference?attach_pdf_sha=<sha> creates a reference
	// and attaches the sha in the same transaction. The query
	// parameter mirrors what the WUI's reference-add modal uses.
	u := fmt.Sprintf("%s/api/reference?attach_pdf_sha=%s", server, up.SHA512)
	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func setLicense(client *http.Client, server, refID, license, sourceURL string) error {
	body, _ := json.Marshal(map[string]any{
		"license":    license,
		"source_url": sourceURL,
	})
	req, err := http.NewRequest("PUT",
		fmt.Sprintf("%s/api/reference/%s/license", server, refID),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func orEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func init() {
	ingestCmd.Flags().String("server", "http://127.0.0.1:2365", "base URL of the running hive server")
	ingestCmd.Flags().String("license", "", "license to record on each reference (CC-BY, CC0, PD, PD-BHL, other-verified, unknown)")
	ingestCmd.Flags().String("license-source", "", "URL where the license was verified (optional)")
	ingestCmd.Flags().Bool("quiet", false, "suppress per-file progress output")
	rootCmd.AddCommand(ingestCmd)
}
