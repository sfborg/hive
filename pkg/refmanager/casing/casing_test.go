package casing

import "testing"

func TestVocabulary(t *testing.T) {
	v := Vocabulary([]string{
		"The genus Fictogenus was described from Austria. Sequences of rbcL and DNA were compared.",
		"Fictogenus alpha grows in Austria; the alpha form differs. Methods follow standard practice.",
		"MATERIALS AND METHODS",
		"We studied the Methods of earlier work.",
		// A running head embedded in a paragraph.
		"as shown PHYLOGENETIC POSITION OF FICTOGENUS in the position of the cells.",
		"An ABSTRACTION is not an acronym.",
	})
	want := map[string]string{
		"fictogenus": "Fictogenus",
		"austria":    "Austria",
		"rbcl":       "rbcL",
		"dna":        "DNA",
	}
	for low, form := range want {
		if v[low] != form {
			t.Errorf("v[%q] = %q, want %q", low, v[low], form)
		}
	}
	// "materials" appears only in a capitals segment; "sequences" only
	// at a sentence start: neither is evidence.
	for _, low := range []string{"the", "alpha", "materials", "sequences", "position", "phylogenetic", "abstraction"} {
		if f, ok := v[low]; ok {
			t.Errorf("v[%q] = %q, want absent", low, f)
		}
	}
}

func TestShouting(t *testing.T) {
	for s, want := range map[string]bool{
		"PHYLOGENETIC POSITION OF ZYGOGONIUM": true,
		"Phylogenetic position of Zygogonium": false,
		"DNA":                                 false, // too short to judge
		"rbcL and atpB":                       false,
	} {
		if got := Shouting(s); got != want {
			t.Errorf("Shouting(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestJATSSegments(t *testing.T) {
	jats := []byte(`<article><front><article-meta><title-group><article-title>FICTOGENUS TITLE</article-title></title-group>
<abstract><p>Fictogenus occurs in Austria &amp; nearby.</p></abstract></article-meta></front>
<body><sec><title>INTRODUCTION</title><p>The genus Fictogenus.</p></sec></body>
<back><ref-list><ref>Cited Work Title</ref></ref-list></back></article>`)
	segs, err := JATSSegments(jats)
	if err != nil {
		t.Fatalf("JATSSegments: %v", err)
	}
	want := []string{"Fictogenus occurs in Austria & nearby.", "The genus Fictogenus."}
	if len(segs) != len(want) {
		t.Fatalf("segments = %q, want %q", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, segs[i], want[i])
		}
	}
}
