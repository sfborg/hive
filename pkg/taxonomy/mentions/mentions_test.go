package mentions

import (
	"context"
	"testing"

	vlib "github.com/gnames/gnlib/ent/verifier"
)

const sampleJATS = `<article xmlns:tp="http://www.plazi.org/taxpub" xmlns:gn="https://globalnames.org">
<body><p>
<tp:taxon-name full-name="Fictogenus alpha" gn:source="verified" gn:score="9.4">Fictogenus alpha</tp:taxon-name>
and <tp:taxon-name full-name="Fictogenus  alpha" gn:source="verified">F. alpha</tp:taxon-name>
near <tp:taxon-name full-name="Fictogenus" gn:source="verified">Fictogenus</tp:taxon-name>;
not resolved: <tp:taxon-name full-name="F. beta" gn:source="unresolved">F. beta</tp:taxon-name>
publisher: <tp:taxon-name><tp:taxon-name-part taxon-name-part-type="genus">Fictogenus</tp:taxon-name-part> <tp:taxon-name-part taxon-name-part-type="species">alpha</tp:taxon-name-part></tp:taxon-name>
publisher class: <tp:taxon-name><tp:taxon-name-part>Fictophyceae</tp:taxon-name-part></tp:taxon-name>
&nbsp;and <tp:taxon-name full-name="Inventa nova" gn:source="unverified">Inventa nova</tp:taxon-name>
</p></body></article>`

func TestCount(t *testing.T) {
	got, err := Count([]byte(sampleJATS))
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	want := map[string]int{"Fictogenus alpha": 3, "Fictogenus": 1, "Inventa nova": 1, "Fictophyceae": 1}
	if len(got) != len(want) {
		t.Fatalf("Count = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Count[%q] = %d, want %d", k, got[k], v)
		}
	}
}

func TestMergeKeepsHighestCount(t *testing.T) {
	got := Merge(map[string]int{"A": 3, "B": 1}, map[string]int{"A": 2, "C": 4})
	if got["A"] != 3 || got["B"] != 1 || got["C"] != 4 || len(got) != 3 {
		t.Errorf("Merge = %v", got)
	}
}

type stubVerifier struct {
	calls [][]string
}

func (s *stubVerifier) VerifyBatch(_ context.Context, names []string) []vlib.Name {
	s.calls = append(s.calls, names)
	out := make([]vlib.Name, len(names))
	for i, n := range names {
		out[i] = vlib.Name{Name: n}
		if n == "Fictogenus alpha" {
			out[i].MatchType = vlib.PartialExact
			out[i].BestResult = &vlib.ResultData{
				MatchedCanonicalSimple: "Fictogenus",
				CurrentCanonicalSimple: "Fictogenus",
				ClassificationPath:     "Plantae|Fictophyta|Fictaceae|Fictogenus",
				ClassificationRanks:    "kingdom|phylum|family|genus",
				ClassificationIDs:      "k1|p1|f1|g1",
			}
		}
	}
	return out
}

func (s *stubVerifier) DataSource(id int) (vlib.DataSource, error) {
	return vlib.DataSource{ID: id, RevisionDate: "2026-08-20", UpdatedAt: "2026-09-09"}, nil
}

func TestVerify(t *testing.T) {
	v := &stubVerifier{}
	got := Verify(context.Background(), v, []string{"Fictogenus alpha", "Inventa nova"})
	m, ok := got["Fictogenus alpha"]
	if !ok || len(got) != 1 {
		t.Fatalf("Verify = %+v", got)
	}
	if m.RecordID != "g1" || m.MatchType != "PartialExact" || len(m.Path) != 4 {
		t.Errorf("match = %+v", m)
	}
	if m.Path[2] != (Rank{Name: "Fictaceae", Rank: "family", ID: "f1"}) {
		t.Errorf("path[2] = %+v", m.Path[2])
	}
}

type futureDateVerifier struct{ stubVerifier }

func (*futureDateVerifier) DataSource(id int) (vlib.DataSource, error) {
	return vlib.DataSource{ID: id, RevisionDate: "2999-01-01", UpdatedAt: "2026-09-28"}, nil
}

func TestSourceTagSkipsFutureReleaseDate(t *testing.T) {
	tag, err := SourceTag(&futureDateVerifier{}, NCBI)
	if err != nil || tag != "gnverifier:4@2026-09-28" {
		t.Errorf("SourceTag = %q, %v", tag, err)
	}
}

func TestSourceTagPrefersReleaseDate(t *testing.T) {
	tag, err := SourceTag(&stubVerifier{}, CatalogueOfLife)
	if err != nil || tag != "gnverifier:1@2026-08-20" {
		t.Errorf("SourceTag = %q, %v", tag, err)
	}
}
