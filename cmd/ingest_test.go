package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIngestBasename covers the grouping heuristic against the
// real filename patterns hive's users produce — publisher
// filename conventions (Pensoft, MDPI, Frontiers, Wiley, PLoS,
// Nature, PeerJ, BHL originals) plus the browser download-
// duplicate suffix pattern.
func TestIngestBasename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Multi-format group — all three should share the base.
		{"mdpi pdf", "plants-12-01835.pdf", "plants-12-01835"},
		{"mdpi xml", "plants-12-01835.xml", "plants-12-01835"},
		{"mdpi epub", "plants-12-01835.epub", "plants-12-01835"},

		// MDPI versioned corrections.
		{"mdpi -v2", "plants-12-01835-v2.pdf", "plants-12-01835"},
		{"mdpi -v3", "plants-14-01272-v3.pdf", "plants-14-01272"},

		// MDPI slug ending in -\d+ should NOT get stripped.
		{"mdpi slug preserved", "plants-11-02445.pdf", "plants-11-02445"},

		// Compound extensions.
		{"jats.xml", "Ehrenberg_1830.jats.xml", "Ehrenberg_1830"},
		{"tei.xml", "Ehrenberg_1830.tei.xml", "Ehrenberg_1830"},
		{"grobid.xml", "Ehrenberg_1830.grobid.xml", "Ehrenberg_1830"},

		// Pensoft filename with _en_1 language variant tail —
		// must NOT get stripped (that's not a version suffix).
		{"pensoft with _en_1", "PK-268-103_article-178310_en_1.pdf",
			"PK-268-103_article-178310_en_1"},

		// Frontiers, Nature, PLoS.
		{"frontiers", "fmicb-16-1701605.pdf", "fmicb-16-1701605"},
		{"nature srep", "srep25367.pdf", "srep25367"},
		{"plos one", "journal.pone.0333782.pdf", "journal.pone.0333782"},

		// Journal of Phycology (Wiley) — long title.
		{"wiley long", "Journal of Phycology - 2025 - Knotek - Expanding.pdf",
			"Journal of Phycology - 2025 - Knotek - Expanding"},

		// BHL originals — plain word runs, no publisher pattern.
		{"bhl 1", "abhandlungenderk18411deut.pdf", "abhandlungenderk18411deut"},
		{"bhl 2", "mobot31753000798865.pdf", "mobot31753000798865"},

		// -v20260913 has too many digits to be a version number;
		// leave it alone.
		{"long numeric suffix left alone",
			"paper-v20260913.pdf", "paper-v20260913"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ingestBasename(tc.in)
			if got != tc.want {
				t.Errorf("ingestBasename(%q) = %q, want %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestExpandIngestArgs covers the directory-walking behavior:
// flat-directory reads (default), --recurse walks, individual
// files pass through, unsupported extensions are filtered, and
// duplicate args dedupe.
func TestExpandIngestArgs(t *testing.T) {
	dir := t.TempDir()
	// Flat entries at the top level.
	touch(t, filepath.Join(dir, "paper1.pdf"))
	touch(t, filepath.Join(dir, "paper1.xml"))
	touch(t, filepath.Join(dir, "notes.txt")) // unsupported
	// One paper in a subdirectory.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(sub, "paper2.pdf"))
	touch(t, filepath.Join(sub, "paper2.jats.xml"))

	// Non-recursive walk picks up top-level only.
	got, err := expandIngestArgs([]string{dir}, false)
	if err != nil {
		t.Fatalf("non-recurse: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("non-recurse got %d, want 2 (paper1.pdf + paper1.xml)", len(got))
	}

	// Recursive walk picks up the sub-dir too.
	got, err = expandIngestArgs([]string{dir}, true)
	if err != nil {
		t.Fatalf("recurse: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("recurse got %d, want 4 (paper1.pdf, paper1.xml, sub/paper2.pdf, sub/paper2.jats.xml)", len(got))
	}

	// Individual file arg with dir arg — no duplicate.
	got, err = expandIngestArgs(
		[]string{dir, filepath.Join(dir, "paper1.pdf")}, false)
	if err != nil {
		t.Fatalf("dedup: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("dedup got %d, want 2", len(got))
	}

	// Individual file arg only.
	got, err = expandIngestArgs(
		[]string{filepath.Join(dir, "paper1.xml")}, false)
	if err != nil {
		t.Fatalf("single-file: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("single-file got %d, want 1", len(got))
	}

	// Unsupported extension is filtered on directory walk (not on
	// direct file args — those pass through).
	got, err = expandIngestArgs([]string{filepath.Join(dir, "notes.txt")}, false)
	if err != nil {
		t.Fatalf("txt direct: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("txt direct got %d, want 1 (pass through)", len(got))
	}
}

// TestIsSupportedIngestExt covers the extension whitelist.
func TestIsSupportedIngestExt(t *testing.T) {
	cases := map[string]bool{
		"paper.pdf":       true,
		"paper.PDF":       true,
		"paper.xml":       true,
		"paper.jats.xml":  true,
		"paper.epub":      true,
		// HTML is deliberately unsupported — publisher HTML is
		// per-site templated with client-side-loaded body content
		// and there is no viable general conversion path.
		"paper.html":      false,
		"paper.htm":       false,
		"paper.txt":       false,
		"paper.docx":      false,
		"paper":           false,
		".hiddenfile.pdf": true,
	}
	for name, want := range cases {
		if got := isSupportedIngestExt(name); got != want {
			t.Errorf("isSupportedIngestExt(%q) = %v, want %v", name, got, want)
		}
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestGroupIngestFiles verifies that the -v2 MDPI case actually
// collapses into a single group — the whole point of the fix.
func TestGroupIngestFiles(t *testing.T) {
	files := []string{
		"plants-12-01835.pdf",
		"plants-12-01835.xml",
		"plants-12-01835.epub",
		"plants-12-01835-v2.pdf",
		"plants-11-02445.pdf",         // different paper
		"plants-11-02445-v2.pdf",      // its correction
		"Ehrenberg_1830.jats.xml",
		"Ehrenberg_1830.pdf",
	}
	groups := groupIngestFiles(files)
	byBase := make(map[string][]string, len(groups))
	for _, g := range groups {
		byBase[g.base] = g.files
	}
	// plants-12-01835 should have 4 files (pdf, xml, epub, -v2).
	if got := len(byBase["plants-12-01835"]); got != 4 {
		t.Errorf("plants-12-01835 group size = %d, want 4 (got %v)",
			got, byBase["plants-12-01835"])
	}
	// plants-11-02445 should have 2 files.
	if got := len(byBase["plants-11-02445"]); got != 2 {
		t.Errorf("plants-11-02445 group size = %d, want 2 (got %v)",
			got, byBase["plants-11-02445"])
	}
	// Ehrenberg group.
	if got := len(byBase["Ehrenberg_1830"]); got != 2 {
		t.Errorf("Ehrenberg_1830 group size = %d, want 2 (got %v)",
			got, byBase["Ehrenberg_1830"])
	}
	if got := len(groups); got != 3 {
		t.Errorf("group count = %d, want 3 distinct papers", got)
	}
}
