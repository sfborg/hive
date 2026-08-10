package hive

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCreateOpenSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")

	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.IsReadOnly() {
		t.Fatal("newly created archive should not be read-only")
	}
	if a.Path() != path {
		t.Fatalf("Path() = %q, want %q", a.Path(), path)
	}

	v, err := a.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("SchemaVersion() = %q, want %q", v, SchemaVersion)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reopened.Close()
	v2, err := reopened.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion (reopen): %v", err)
	}
	if v2 != v {
		t.Fatalf("reopened version %q != original %q", v2, v)
	}
}

func TestCreateRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a.Close()

	_, err = Create(path)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Create over existing file: got %v, want ErrExists", err)
	}
}

func TestOpenMissingReturnsNotFound(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "nope.db"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open on missing file: got %v, want ErrNotFound", err)
	}
}

func TestWithTxCommitAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	ctx := WithActor(context.Background(), "tester@example.org")

	// Commit path — insert a metadata row and confirm it persists.
	err = a.WithTx(ctx, func(tx *Tx) error {
		if tx.Actor() != "tester@example.org" {
			t.Fatalf("Tx.Actor() = %q, want %q", tx.Actor(), "tester@example.org")
		}
		_, err := tx.tx.ExecContext(ctx,
			`INSERT INTO metadata (col__title) VALUES (?)`, "test dataset")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx (commit): %v", err)
	}

	var count int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metadata WHERE col__title = 'test dataset'`).Scan(&count); err != nil {
		t.Fatalf("count after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed row not visible: count=%d", count)
	}

	// Rollback path — an error from fn must abort the transaction.
	sentinel := errors.New("boom")
	err = a.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx,
			`INSERT INTO metadata (col__title) VALUES (?)`, "should rollback"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx returned %v, want sentinel", err)
	}
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metadata WHERE col__title = 'should rollback'`).Scan(&count); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back row was persisted: count=%d", count)
	}
}

func TestReadOnlyRefusesWithTx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	a.Close()

	ro, err := Open(path, ReadOnly())
	if err != nil {
		t.Fatalf("Open ReadOnly: %v", err)
	}
	defer ro.Close()

	if !ro.IsReadOnly() {
		t.Fatal("ReadOnly() archive reports IsReadOnly() == false")
	}
	err = ro.WithTx(context.Background(), func(*Tx) error { return nil })
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("WithTx on RO archive: got %v, want ErrReadOnly", err)
	}
}

func TestActorFromContextEmptyByDefault(t *testing.T) {
	if a := ActorFromContext(context.Background()); a != "" {
		t.Fatalf("ActorFromContext default = %q, want empty", a)
	}
	ctx := WithActor(context.Background(), "alice")
	if a := ActorFromContext(ctx); a != "alice" {
		t.Fatalf("ActorFromContext = %q, want alice", a)
	}
}

// TestSflibHandleAvailableOnReadWrite verifies the sflib integration: a
// hive-created archive should expose a working sflib handle whose Version()
// matches what SchemaVersion() returns. A read-only archive should not.
// This is the smoke test for the sflib pivot — if this fails, the wrapping
// is off.
func TestSflibHandleAvailableOnReadWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	a, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer a.Close()

	if a.Sflib() == nil {
		t.Fatal("Sflib() returned nil on read-write archive")
	}
	sfVersion := a.Sflib().Version()
	if sfVersion == "" {
		t.Fatal("sflib Version() returned empty on hive-created archive")
	}
	hiveVersion, err := a.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if sfVersion != hiveVersion {
		t.Fatalf("sflib Version=%q vs hive SchemaVersion=%q — should agree",
			sfVersion, hiveVersion)
	}

	// Read-only path skips sflib entirely by design.
	a.Close()
	ro, err := Open(path, ReadOnly())
	if err != nil {
		t.Fatalf("Open ReadOnly: %v", err)
	}
	defer ro.Close()
	if ro.Sflib() != nil {
		t.Fatal("Sflib() returned non-nil on read-only archive; expected nil")
	}
}
