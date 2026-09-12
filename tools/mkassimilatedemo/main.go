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
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/sflib/pkg/coldp"
)

const (
	seedPath = "testdata/assimilate_demo/existing_tree.sql"
	jatsPath = "testdata/assimilate_demo/mini_monograph.jats.xml"
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

	fmt.Printf("wrote %s\n", path)
	fmt.Printf("attached paper: %s (sha %s...)\n", jatsPath, ing.SHA512[:16])
	fmt.Println()
	fmt.Println("next:")
	// When path lives under ./data/ the docker-up recipe takes just
	// the archive basename; otherwise the caller passed a custom
	// path and has to arrange their own docker mount.
	fmt.Printf("  just docker-down && just docker-up %s\n", filepath.Base(path))
	fmt.Println("  open http://localhost:2365 — reference is under References")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
