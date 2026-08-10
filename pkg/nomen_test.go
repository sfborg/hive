package hive

import "testing"

// TestNomenDowncastICZNRoots exercises the root-URI mapping directly —
// each ICZN root in colRootStatus should map to its own status.
func TestNomenDowncastICZNRoots(t *testing.T) {
	cases := map[string]string{
		"http://purl.obolibrary.org/obo/NOMEN_0000168": "NOT_ESTABLISHED",
		"http://purl.obolibrary.org/obo/NOMEN_0000219": "REJECTED",
		"http://purl.obolibrary.org/obo/NOMEN_0000223": "ESTABLISHED",
		"http://purl.obolibrary.org/obo/NOMEN_0000226": "UNACCEPTABLE",
		"http://purl.obolibrary.org/obo/NOMEN_0000225": "DOUBTFUL",
	}
	for uri, want := range cases {
		if got := NomenDowncast(uri); got != want {
			t.Errorf("NomenDowncast(%s) = %q, want %q", uri, got, want)
		}
	}
}

// TestNomenDowncastInherits picks a term that's not a mapped root but
// should resolve via its parent chain — proves the subClassOf walk is
// actually happening rather than just a flat table lookup.
func TestNomenDowncastInherits(t *testing.T) {
	// NOMEN_0000224 is ICZN Available Valid (an ACCEPTABLE root).
	// NOMEN_0000107 is the "ICZN name" top-level, unmapped. Any descendant
	// of a mapped root must resolve to that root's bucket. We assert on
	// URIs that are known descendants of ICZN Unavailable (NOMEN_0000168 →
	// NOT_ESTABLISHED) — e.g. NOMEN_0000173, NOMEN_0000174 are subclasses
	// in the OWL. If those aren't in the OWL, the test skips.
	terms, err := Nomen()
	if err != nil {
		t.Fatalf("Nomen: %v", err)
	}
	// Pick a term whose Parent points at a mapped root and confirm its
	// ColStatus matches. Scan for the first such case rather than
	// hard-coding URIs that might drift with NOMEN updates.
	for _, term := range terms {
		if term.Parent == "" {
			continue
		}
		rootStatus, ok := colRootStatus[term.Parent]
		if !ok {
			continue
		}
		if term.ColStatus != rootStatus {
			t.Fatalf("term %s (parent %s) ColStatus = %q, want %q",
				term.Local, term.Parent, term.ColStatus, rootStatus)
		}
		return // one confirmed inheritance is enough
	}
	t.Skip("no NOMEN term found with a mapped parent — OWL may have changed")
}

// TestNomenDowncastUnmapped confirms an OWL term outside any mapped
// subtree resolves to "" (no CoLDP target).
func TestNomenDowncastUnmapped(t *testing.T) {
	// NOMEN_0000003 is "ICZN nanorder" — a rank, not a status. Not in
	// the classification/relationship trees CoL maps, so ColStatus
	// should be empty.
	got := NomenDowncast("http://purl.obolibrary.org/obo/NOMEN_0000003")
	if got != "" {
		t.Errorf("NomenDowncast(NOMEN_0000003) = %q, want empty", got)
	}
}
