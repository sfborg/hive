package hive

import (
	"context"
	"errors"
	"testing"
)

func TestTestSetAddRemoveIsPresent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	in, err := a.IsInTestSet(ctx, "ref-1")
	if err != nil {
		t.Fatalf("IsInTestSet before: %v", err)
	}
	if in {
		t.Errorf("expected false before add")
	}

	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.AddToTestSet("ref-1", "part of the Dascilloidea gold set")
	})
	if err != nil {
		t.Fatalf("AddToTestSet: %v", err)
	}

	in, _ = a.IsInTestSet(ctx, "ref-1")
	if !in {
		t.Errorf("expected true after add")
	}

	m, err := a.GetTestSetMembership(ctx, "ref-1")
	if err != nil {
		t.Fatalf("GetTestSetMembership: %v", err)
	}
	if m.AddedBy != "0000-0000-0000-0000" {
		t.Errorf("AddedBy = %q", m.AddedBy)
	}
	if m.Notes != "part of the Dascilloidea gold set" {
		t.Errorf("Notes = %q", m.Notes)
	}
	if m.AddedAt.IsZero() {
		t.Errorf("AddedAt not stamped")
	}

	// Remove.
	err = a.WithTx(ctx, func(tx *Tx) error {
		return tx.RemoveFromTestSet("ref-1")
	})
	if err != nil {
		t.Fatalf("RemoveFromTestSet: %v", err)
	}
	in, _ = a.IsInTestSet(ctx, "ref-1")
	if in {
		t.Errorf("expected false after remove")
	}

	// GetTestSetMembership returns ErrNotFound.
	if _, err := a.GetTestSetMembership(ctx, "ref-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestTestSetAddIsIdempotent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	// Two adds don't error out; second doesn't create a duplicate.
	err := a.WithTx(ctx, func(tx *Tx) error {
		if err := tx.AddToTestSet("ref-idem", "first"); err != nil {
			return err
		}
		return tx.AddToTestSet("ref-idem", "")
	})
	if err != nil {
		t.Fatalf("double add: %v", err)
	}

	members, _ := a.ListTestSetMembers(ctx)
	if len(members) != 1 {
		t.Errorf("expected 1 row after double-add, got %d", len(members))
	}
	// Empty notes on second add preserved the first's notes.
	m, _ := a.GetTestSetMembership(ctx, "ref-idem")
	if m.Notes != "first" {
		t.Errorf("Notes = %q, want first preserved", m.Notes)
	}
}

func TestTestSetRemoveIsIdempotent(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	err := a.WithTx(ctx, func(tx *Tx) error {
		return tx.RemoveFromTestSet("never-added")
	})
	if err != nil {
		t.Fatalf("remove of missing row should be no-op, got: %v", err)
	}
}

func TestListTestSetMembers(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")

	err := a.WithTx(ctx, func(tx *Tx) error {
		for _, id := range []string{"a", "b", "c"} {
			if err := tx.AddToTestSet("ref-"+id, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	members, err := a.ListTestSetMembers(ctx)
	if err != nil {
		t.Fatalf("ListTestSetMembers: %v", err)
	}
	if len(members) != 3 {
		t.Errorf("got %d members, want 3", len(members))
	}
}
