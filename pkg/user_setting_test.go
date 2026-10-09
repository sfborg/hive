package hive

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestUserSettingsArePerActor(t *testing.T) {
	a := newTestArchive(t)
	alice := WithActor(context.Background(), "0000-0000-0000-0001")
	bob := WithActor(context.Background(), "0000-0000-0000-0002")
	set := func(ctx context.Context, key, value string) error {
		return a.WithTx(ctx, func(tx *Tx) error { return tx.SetUserSetting(key, json.RawMessage(value)) })
	}
	if err := set(alice, "references.columns", `["title","year"]`); err != nil {
		t.Fatal(err)
	}
	if err := set(alice, "references.columns", `["year"]`); err != nil {
		t.Fatal(err)
	}
	if err := set(bob, "theme", `"dark"`); err != nil {
		t.Fatal(err)
	}
	got, err := a.UserSettings(alice, "0000-0000-0000-0001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got["references.columns"]) != `["year"]` {
		t.Errorf("alice settings = %v", got)
	}
	if err := set(alice, "bad", `{not json`); !errors.Is(err, ErrValidation) {
		t.Errorf("invalid JSON: got %v, want ErrValidation", err)
	}
	if err := a.WithTx(alice, func(tx *Tx) error { return tx.DeleteUserSetting("references.columns") }); err != nil {
		t.Fatal(err)
	}
	got, _ = a.UserSettings(alice, "0000-0000-0000-0001")
	if len(got) != 0 {
		t.Errorf("after delete: %v", got)
	}
}

func TestMeasureJATS(t *testing.T) {
	doc := `<p>One <italic>two</italic> three&amp;four<!-- removed text --></p>`
	words, chars := MeasureJATS([]byte(doc))
	if words != 3 {
		t.Errorf("words = %d, want 3", words)
	}
	if chars != len(doc) {
		t.Errorf("characters = %d, want %d", chars, len(doc))
	}
}
