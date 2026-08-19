package sidecar

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	cases := []struct {
		archive, want string
	}{
		{"/tmp/foo.db", "/tmp/foo.pdfs"},
		{"/tmp/foo.DB", "/tmp/foo.pdfs"},
		{"/tmp/foo", "/tmp/foo.pdfs"},
		{"relative/path.db", "relative/path.pdfs"},
	}
	for _, c := range cases {
		got := Dir(c.archive)
		if got != c.want {
			t.Errorf("Dir(%q) = %q, want %q", c.archive, got, c.want)
		}
	}
}

func TestIngestBasic(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")
	src := bytes.NewBufferString("hello world")

	res, err := Ingest(archive, src, "pdf", "application/pdf", "")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.AlreadyExisted {
		t.Errorf("first ingest reported AlreadyExisted=true")
	}
	if res.Size != 11 {
		t.Errorf("Size = %d, want 11", res.Size)
	}
	if res.Ext != "pdf" {
		t.Errorf("Ext = %q, want pdf", res.Ext)
	}
	if len(res.SHA512) != 128 {
		t.Errorf("SHA512 length = %d, want 128 (hex-encoded 64 bytes)", len(res.SHA512))
	}
	// The stored file should exist and read back as the original.
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("stored content = %q, want %q", string(got), "hello world")
	}
	// Meta file should exist.
	meta, err := ReadMeta(archive, res.SHA512)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta == nil {
		t.Fatalf("meta not written")
	}
	if meta.SHA512 != res.SHA512 {
		t.Errorf("meta.SHA512 = %q, want %q", meta.SHA512, res.SHA512)
	}
}

func TestIngestDedup(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")

	// First ingest.
	first, err := Ingest(archive, bytes.NewBufferString("same content"), "pdf", "", "")
	if err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	if first.AlreadyExisted {
		t.Fatalf("first ingest should not report AlreadyExisted")
	}

	// Second ingest with identical bytes → dedup.
	second, err := Ingest(archive, bytes.NewBufferString("same content"), "pdf", "", "")
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if !second.AlreadyExisted {
		t.Fatalf("duplicate ingest should report AlreadyExisted=true")
	}
	if second.SHA512 != first.SHA512 {
		t.Errorf("dedup SHA mismatch: %q vs %q", second.SHA512, first.SHA512)
	}
	if second.Path != first.Path {
		t.Errorf("dedup path mismatch: %q vs %q", second.Path, first.Path)
	}
}

func TestIngestDifferentContentDifferentSha(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")
	a, _ := Ingest(archive, bytes.NewBufferString("A"), "pdf", "", "")
	b, _ := Ingest(archive, bytes.NewBufferString("B"), "pdf", "", "")
	if a.SHA512 == b.SHA512 {
		t.Fatalf("different content produced same SHA — hash function broken?")
	}
	// Both hex-decodable.
	if _, err := hex.DecodeString(a.SHA512); err != nil {
		t.Errorf("SHA512 not hex-decodable: %v", err)
	}
}

func TestStoreAndRead(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")
	// Ingest a source file first so the sidecar dir exists.
	res, err := Ingest(archive, bytes.NewBufferString("source"), "pdf", "", "")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// Store a derivation.
	if err := Store(archive, res.SHA512, "tei.xml", []byte("<TEI/>")); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// Read it back.
	got, err := Read(archive, res.SHA512, "tei.xml")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "<TEI/>" {
		t.Errorf("Read = %q, want %q", string(got), "<TEI/>")
	}
	// Overwrite is fine — derivations are re-runnable.
	if err := Store(archive, res.SHA512, "tei.xml", []byte("<TEI>new</TEI>")); err != nil {
		t.Fatalf("Store overwrite: %v", err)
	}
	got, _ = Read(archive, res.SHA512, "tei.xml")
	if string(got) != "<TEI>new</TEI>" {
		t.Errorf("overwrite read = %q", string(got))
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")
	data, err := Read(archive, strings.Repeat("a", 128), "tei.xml")
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if data != nil {
		t.Errorf("Read missing returned non-nil: %q", string(data))
	}
}

func TestReadMetaMissing(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.db")
	meta, err := ReadMeta(archive, strings.Repeat("a", 128))
	if err != nil {
		t.Fatalf("ReadMeta missing: %v", err)
	}
	if meta != nil {
		t.Errorf("ReadMeta missing returned non-nil")
	}
}
