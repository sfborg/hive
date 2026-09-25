package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// servicesCheckTimeout bounds the GET /api/services call. The server
// spends at most a few seconds pinging GROBID and OCR.
const servicesCheckTimeout = 15 * time.Second

// ingestServices is the part of the GET /api/services response that
// ingest acts on.
type ingestServices struct {
	GROBID string `json:"grobid"`
	Pandoc string `json:"pandoc"`
}

// fetchServices reads the server's document-processing services. A
// server without the endpoint returns (nil, nil), so ingest proceeds
// without a warning. An unreachable server is an error.
func fetchServices(client *http.Client, server string) (*ingestServices, error) {
	ctx, cancel := context.WithTimeout(context.Background(), servicesCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/services", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the hive server at %s: %w", server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET /api/services: HTTP %d", resp.StatusCode)
	}
	var out ingestServices
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("GET /api/services: %w", err)
	}
	return &out, nil
}

// serviceWarnings returns the warnings to show before uploading a batch
// with the given number of PDF and EPUB files. Empty means the server
// can process everything in the batch.
func serviceWarnings(svc ingestServices, server string, pdfs, epubs int) []string {
	var out []string
	if pdfs > 0 && svc.GROBID != "ok" {
		var why, fix string
		if svc.GROBID == "unavailable" {
			why = "has GROBID configured, but GROBID is not responding"
			fix = "Check that the Docker stack is running (docker compose ps)."
		} else {
			why = "is not connected to GROBID"
			fix = "To process PDFs, run hive with the Docker stack (just docker-up);\n   see the README."
		}
		out = append(out, fmt.Sprintf(
			"PDF processing is not available.\n"+
				"   The hive server at %s %s.\n"+
				"   The %d PDF file(s) in this batch will be stored without extracting\n"+
				"   metadata, a DOI or text.\n"+
				"   %s", server, why, pdfs, fix))
	}
	if epubs > 0 && svc.Pandoc != "ok" {
		out = append(out, fmt.Sprintf(
			"EPUB conversion is not available: pandoc is not installed where the\n"+
				"   hive server runs. The %d EPUB file(s) in this batch will be stored\n"+
				"   with metadata only.", epubs))
	}
	return out
}

// countByExt counts the PDF and EPUB files in a batch.
func countByExt(files []string) (pdfs, epubs int) {
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".pdf":
			pdfs++
		case ".epub":
			epubs++
		}
	}
	return pdfs, epubs
}

// checkIngestServices warns when the server can't process some of the
// batch and, unless yes is set, asks whether to continue. An empty or
// unreadable answer counts as no.
func checkIngestServices(client *http.Client, server string, files []string, yes bool) error {
	svc, err := fetchServices(client, server)
	if err != nil || svc == nil {
		return err
	}
	pdfs, epubs := countByExt(files)
	warnings := serviceWarnings(*svc, server, pdfs, epubs)
	if len(warnings) == 0 {
		return nil
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "⚠  %s\n\n", w)
	}
	if yes {
		return nil
	}
	ans, _ := prompt(bufio.NewReader(os.Stdin), "Continue anyway? (yes/no): ")
	ans = strings.ToLower(ans)
	if ans != "yes" && ans != "y" {
		return fmt.Errorf("aborted by user")
	}
	return nil
}
