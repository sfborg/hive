package hive

import (
	"context"
	"errors"
	"testing"
)

// TestAgentCRUD covers the create → get → list → update → delete
// cycle on every role and the validation constraints that differ
// by role (contact requires email; publisher allows empty names).
func TestAgentCRUD(t *testing.T) {
	ctx := context.Background()
	a := newTestArchive(t)
	defer a.Close()

	// Create one agent per role and verify it round-trips.
	newIDs := make(map[Role]int)
	for _, role := range AllRoles {
		role := role
		t.Run(string(role)+"-create-get", func(t *testing.T) {
			ag := Agent{
				Role:         role,
				Orcid:        "0000-0002-1825-0097",
				Given:        "Josiah",
				Family:       "Carberry",
				RorID:        "05gq02987",
				Organisation: "Brown University",
				Email:        "carberry@example.org",
				Note:         "test note",
			}
			var newID int
			err := a.WithTx(ctx, func(tx *Tx) error {
				id, err := tx.CreateAgent(ag)
				if err != nil {
					return err
				}
				newID = id
				return nil
			})
			if err != nil {
				t.Fatalf("create %s: %v", role, err)
			}
			if newID == 0 {
				t.Fatalf("expected non-zero new id for %s", role)
			}
			newIDs[role] = newID

			got, err := a.GetAgent(ctx, role, newID)
			if err != nil {
				t.Fatalf("get %s %d: %v", role, newID, err)
			}
			if got.Family != "Carberry" || got.Given != "Josiah" {
				t.Errorf("round-trip failed for %s: got %+v", role, got)
			}
			if got.Role != role {
				t.Errorf("role mismatch: want %s, got %s", role, got.Role)
			}
		})
	}

	// List should include the freshly-created row for each role.
	for _, role := range AllRoles {
		role := role
		t.Run(string(role)+"-list", func(t *testing.T) {
			list, err := a.ListAgents(ctx, role)
			if err != nil {
				t.Fatalf("list %s: %v", role, err)
			}
			found := false
			for _, ag := range list {
				if ag.ID == newIDs[role] {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("newly-created %s agent %d not in list",
					role, newIDs[role])
			}
		})
	}

	// Update a creator, verify the change persists.
	t.Run("update-creator", func(t *testing.T) {
		id := newIDs[RoleCreator]
		err := a.WithTx(ctx, func(tx *Tx) error {
			return tx.UpdateAgent(Agent{
				Role:   RoleCreator,
				ID:     id,
				Given:  "Josiah",
				Family: "Carberry",
				Email:  "updated@example.org",
			})
		})
		if err != nil {
			t.Fatalf("update creator: %v", err)
		}
		got, err := a.GetAgent(ctx, RoleCreator, id)
		if err != nil {
			t.Fatalf("get after update: %v", err)
		}
		if got.Email != "updated@example.org" {
			t.Errorf("update didn't persist: email=%q", got.Email)
		}
	})

	// Delete a contributor; verify it's gone.
	t.Run("delete-contributor", func(t *testing.T) {
		id := newIDs[RoleContributor]
		err := a.WithTx(ctx, func(tx *Tx) error {
			return tx.DeleteAgent(RoleContributor, id)
		})
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		_, err = a.GetAgent(ctx, RoleContributor, id)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})
}

// TestAgentValidationConstraints exercises the per-role NOT NULL
// rules and the role-parsing gate.
func TestAgentValidationConstraints(t *testing.T) {
	ctx := context.Background()
	a := newTestArchive(t)
	defer a.Close()

	tests := []struct {
		name    string
		agent   Agent
		wantErr error
	}{
		{
			name:    "unknown role",
			agent:   Agent{Role: "reviewer", Given: "A", Family: "B"},
			wantErr: ErrValidation,
		},
		{
			name:    "creator without given",
			agent:   Agent{Role: RoleCreator, Family: "Doe"},
			wantErr: ErrValidation,
		},
		{
			name:    "creator without family",
			agent:   Agent{Role: RoleCreator, Given: "Jane"},
			wantErr: ErrValidation,
		},
		{
			name:    "contact without email",
			agent:   Agent{Role: RoleContact, Given: "Jane", Family: "Doe"},
			wantErr: ErrValidation,
		},
		{
			name:    "publisher without names ok",
			agent:   Agent{Role: RolePublisher, Organisation: "Acme Press"},
			wantErr: nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := a.WithTx(ctx, func(tx *Tx) error {
				_, err := tx.CreateAgent(tc.agent)
				return err
			})
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("expected success, got: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("expected %v, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestCopyAgentToRole verifies the quick-copy affordance moves a row
// across role tables and blanks the note when requested.
func TestCopyAgentToRole(t *testing.T) {
	ctx := context.Background()
	a := newTestArchive(t)
	defer a.Close()

	var creatorID int
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateAgent(Agent{
			Role:   RoleCreator,
			Given:  "Jane",
			Family: "Doe",
			Email:  "jane@example.org",
			Note:   "primary curator",
		})
		if err != nil {
			return err
		}
		creatorID = id
		return nil
	})
	if err != nil {
		t.Fatalf("seed creator: %v", err)
	}

	// Copy to contact, blanking the note.
	var contactID int
	err = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CopyAgentToRole(RoleCreator, creatorID, RoleContact, true)
		if err != nil {
			return err
		}
		contactID = id
		return nil
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	got, err := a.GetAgent(ctx, RoleContact, contactID)
	if err != nil {
		t.Fatalf("get copied: %v", err)
	}
	if got.Given != "Jane" || got.Family != "Doe" {
		t.Errorf("copy dropped names: %+v", got)
	}
	if got.Email != "jane@example.org" {
		t.Errorf("copy dropped email: %q", got.Email)
	}
	if got.Note != "" {
		t.Errorf("expected blank note, got %q", got.Note)
	}

	// Copy again without blanking — note should carry over.
	var contactID2 int
	err = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CopyAgentToRole(RoleCreator, creatorID, RoleContact, false)
		if err != nil {
			return err
		}
		contactID2 = id
		return nil
	})
	if err != nil {
		t.Fatalf("copy without blank: %v", err)
	}
	got2, _ := a.GetAgent(ctx, RoleContact, contactID2)
	if got2.Note != "primary curator" {
		t.Errorf("expected note to carry over, got %q", got2.Note)
	}
}

// TestMoveAgentToRole verifies Move relocates an agent to a new
// role table and removes the source row atomically.
func TestMoveAgentToRole(t *testing.T) {
	ctx := context.Background()
	a := newTestArchive(t)
	defer a.Close()

	var editorID int
	err := a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.CreateAgent(Agent{
			Role:   RoleEditor,
			Given:  "Grace",
			Family: "Hopper",
			Email:  "grace@example.org",
			Note:   "past editor",
		})
		if err != nil {
			return err
		}
		editorID = id
		return nil
	})
	if err != nil {
		t.Fatalf("seed editor: %v", err)
	}

	var creatorID int
	err = a.WithTx(ctx, func(tx *Tx) error {
		id, err := tx.MoveAgentToRole(RoleEditor, editorID, RoleCreator, true)
		if err != nil {
			return err
		}
		creatorID = id
		return nil
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if creatorID == 0 {
		t.Fatalf("expected new creator id from Move")
	}

	// Source row should be gone.
	if _, err := a.GetAgent(ctx, RoleEditor, editorID); !errors.Is(err, ErrNotFound) {
		t.Errorf("source editor should be deleted after move; got %v", err)
	}
	// Target row should carry names, note blanked.
	dst, err := a.GetAgent(ctx, RoleCreator, creatorID)
	if err != nil {
		t.Fatalf("get moved: %v", err)
	}
	if dst.Family != "Hopper" || dst.Given != "Grace" {
		t.Errorf("move dropped names: %+v", dst)
	}
	if dst.Note != "" {
		t.Errorf("expected blank note after move, got %q", dst.Note)
	}

	// Same-role move is a validation error (nothing to do).
	err = a.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.MoveAgentToRole(RoleCreator, creatorID, RoleCreator, true)
		return err
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation on same-role move, got %v", err)
	}
}

