// mkassimilatedemo creates the assimilate-demo fixture archive.
// Mirrors tools/mkdemo but seeds the existing DB tree from
// testdata/assimilate_demo/existing_tree.sql AND ingests the fake JATS
// monograph so a "paper" reference with attached JATS shows up in the
// reference-manager UI as if a curator had uploaded it.
//
// Not part of the shipped binary — invoked via `just assimilate-demo`
// or `go run ./tools/mkassimilatedemo [path]`.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/sflib/pkg/coldp"
)

const (
	seedPath       = "testdata/assimilate_demo/existing_tree.sql"
	jatsPath       = "testdata/assimilate_demo/mini_monograph.jats.xml"
	composeRunPath = "testdata/assimilate_demo/compose_run.json"
	// Attribution stamped on the fabricated reference + attachment.
	// Fictional ORCID-shaped placeholder — never routes to a real
	// contributor.
	fixtureActor = "0000-0000-0000-0000"
	// Stable UUID for the fake monograph reference so re-runs of
	// `just assimilate-demo` produce the same reference id every
	// time. Otherwise curator browser state (bookmarks, in-memory
	// URLs, localStorage keyed on reference id) breaks on each
	// re-run with "reference not found". Valid UUIDv4 shape
	// (version 4, variant 8-b-a-9) but a distinctive all-zeros
	// prefix makes it recognizably a fixture id.
	fixtureReferenceID = "00000000-0000-4000-8000-000000000001"
)

func main() {
	path := "./data/assimilate_demo.db"
	if len(os.Args) == 2 {
		path = os.Args[1]
	} else if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: mkassimilatedemo [path]")
		os.Exit(2)
	}

	// Idempotent: delete any existing archive and its sidecar library
	// so re-runs produce a clean fixture.
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	_ = os.RemoveAll(sidecar.Dir(path))

	// Create + close so the seed SQL runs against a settled schema
	// (WAL flushed, no pending pragmas).
	a, err := hive.Create(path)
	if err != nil {
		die("create: %v", err)
	}
	if err := a.Close(); err != nil {
		die("close after create: %v", err)
	}

	// Seed the existing tree.
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		die("read seed %s: %v", seedPath, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		die("open %s: %v", path, err)
	}
	if _, err := db.Exec(string(seed)); err != nil {
		db.Close()
		die("exec seed: %v", err)
	}
	if err := db.Close(); err != nil {
		die("close after seed: %v", err)
	}

	// Ingest the fake monograph. Store as both the source (.xml, the
	// format hive's upload handler would accept for a JATS submission)
	// and the derived JATS at <sha>.jats.xml — since the source IS
	// JATS, the two files are identical. Downstream readers open the
	// derivation path.
	jatsBytes, err := os.ReadFile(jatsPath)
	if err != nil {
		die("read jats %s: %v", jatsPath, err)
	}
	ing, err := sidecar.Ingest(path, bytes.NewReader(jatsBytes), "xml",
		"application/xml", "")
	if err != nil {
		die("ingest jats: %v", err)
	}
	if err := sidecar.Store(path, ing.SHA512, "jats.xml", jatsBytes); err != nil {
		die("store jats derivation: %v", err)
	}

	// Reopen the archive through the hive API for the reference +
	// attachment writes so col__modified / col__modified_by get
	// stamped consistently.
	a2, err := hive.Open(path)
	if err != nil {
		die("open %s: %v", path, err)
	}
	defer a2.Close()

	ctx := hive.WithActor(context.Background(), fixtureActor)
	err = a2.WithTx(ctx, func(tx *hive.Tx) error {
		refID, err := tx.CreateReference(coldp.Reference{
			ID:             fixtureReferenceID,
			Author:         "Faulknerville, P.; Ochoa-Renn, M.",
			Title:          "A revision of the Order Tribbulida, with descriptions of new species and clarification of concept boundaries in Tribbulus and allied genera",
			ContainerTitle: "Iota Systematics",
			Volume:         "41",
			Issue:          "3",
			Page:           "101-148",
			Issued:         "2026",
			DOI:            "10.99999/iota.demo.assimilate",
		})
		if err != nil {
			return fmt.Errorf("create reference: %w", err)
		}
		if err := tx.AttachReferencePDF(refID, ing.SHA512); err != nil {
			return fmt.Errorf("attach jats: %w", err)
		}
		return nil
	})
	if err != nil {
		die("%v", err)
	}

	// Seed the synthetic compose extraction run so a fresh
	// assimilate-demo has enough Layer 1 material for `hive
	// reassemble` to exercise every assembler rule out of the box.
	// The payload lives in compose_run.json (git-committed);
	// overwriting that file with real Sonnet/Opus output makes the
	// fixture semantically richer without touching this tool.
	composeCount, err := seedComposeRun(path)
	if err != nil {
		die("seed compose run: %v", err)
	}

	fmt.Printf("wrote %s\n", path)
	fmt.Printf("attached paper: %s (sha %s...)\n", jatsPath, ing.SHA512[:16])
	fmt.Printf("seeded compose run: %s (%d name usages)\n",
		composeRunPath, composeCount)
	fmt.Println()
	fmt.Println("next:")
	// When path lives under ./data/ the docker-up recipe takes just
	// the archive basename; otherwise the caller passed a custom
	// path and has to arrange their own docker mount.
	fmt.Printf("  just docker-down && just docker-up %s\n", filepath.Base(path))
	fmt.Println("  open http://localhost:2365 — reference is under References")
}

// composeRunFixture is the git-committed envelope wrapping a
// compose extraction run's raw_output JSON. The shape mirrors
// hive__extraction_run's columns so mkassimilatedemo can INSERT
// each field directly. RawOutput is a decoded JSON object so
// contributors editing the file don't have to escape a
// string-inside-string; the seeder re-marshals it before INSERT.
type composeRunFixture struct {
	RunID       string          `json:"run_id"`
	ReferenceID string          `json:"reference_id"`
	Purpose     string          `json:"purpose"`
	SourceKind  string          `json:"source_kind"`
	SourceID    string          `json:"source_id"`
	Notes       string          `json:"notes,omitempty"`
	RawOutput   json.RawMessage `json:"raw_output"`
}

// seedComposeRun reads compose_run.json and INSERTs it into
// hive__extraction_run so `hive reassemble` finds Layer 1
// material on a fresh archive. Returns the name-usage count for
// the tool's summary line. Errors surface a specific message per
// step (read / parse / insert) so the user knows whether it's a
// fixture-content issue or a schema issue.
func seedComposeRun(dbPath string) (int, error) {
	raw, err := os.ReadFile(composeRunPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", composeRunPath, err)
	}
	var fx composeRunFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		return 0, fmt.Errorf("parse %s: %w", composeRunPath, err)
	}
	if fx.RunID == "" || fx.ReferenceID == "" ||
		fx.Purpose == "" || fx.SourceKind == "" || fx.SourceID == "" ||
		len(fx.RawOutput) == 0 {
		return 0, fmt.Errorf(
			"%s missing required fields (run_id, reference_id, purpose, source_kind, source_id, raw_output)",
			composeRunPath)
	}

	// Compact raw_output to a single-line JSON blob so
	// reference-shaped diffs stay readable and downstream parsers
	// don't choke on whitespace variance.
	compact := &bytes.Buffer{}
	if err := json.Compact(compact, fx.RawOutput); err != nil {
		return 0, fmt.Errorf("compact raw_output: %w", err)
	}

	// Optional: pull the name_usages count for the summary line.
	// Unrelated to correctness — a bad shape just yields 0.
	var shape struct {
		NameUsages []struct{} `json:"name_usages"`
	}
	_ = json.Unmarshal(compact.Bytes(), &shape)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `INSERT INTO hive__extraction_run (
	    run_id, reference_id, purpose, source_kind, source_id,
	    envelope_json, raw_output_json, parent_run_id,
	    cost_usd, is_gold_standard, notes, created_at
	) VALUES (?, ?, ?, ?, ?, '', ?, NULL, 0, 0, ?, ?)`
	if _, err := db.Exec(q,
		fx.RunID, fx.ReferenceID, fx.Purpose, fx.SourceKind, fx.SourceID,
		compact.String(), fx.Notes, now,
	); err != nil {
		return 0, fmt.Errorf("insert hive__extraction_run: %w", err)
	}
	return len(shape.NameUsages), nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
