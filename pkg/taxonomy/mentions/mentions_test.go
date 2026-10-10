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

func TestToMatchKeepsHomonymLineages(t *testing.T) {
	animal := &vlib.ResultData{
		MatchType:           vlib.Exact,
		ClassificationPath:  "Metazoa|Cnidaria|Coccomyxidae|Coccomyxa",
		ClassificationRanks: "kingdom|phylum|family|genus",
		ClassificationIDs:   "k9|p9|f9|g9",
	}
	alga := &vlib.ResultData{
		MatchType:           vlib.Exact,
		ClassificationPath:  "Viridiplantae|Chlorophyta|Coccomyxaceae|Coccomyxa",
		ClassificationRanks: "kingdom|phylum|family|genus",
		ClassificationIDs:   "k1|p1|f1|g1",
	}
	strain := &vlib.ResultData{
		MatchType:           vlib.Exact,
		ClassificationPath:  "Viridiplantae|Chlorophyta|Coccomyxaceae|Coccomyxa|Coccomyxa sp. C-169",
		ClassificationRanks: "kingdom|phylum|family|genus|strain",
		ClassificationIDs:   "k1|p1|f1|g1|s1",
	}
	fuzzy := &vlib.ResultData{
		MatchType:           vlib.Fuzzy,
		ClassificationPath:  "Metazoa|Arthropoda|Coccidae|Coccomyia",
		ClassificationRanks: "kingdom|phylum|family|genus",
		ClassificationIDs:   "k9|p8|f8|g8",
	}
	m, ok := toMatch(vlib.Name{Name: "Coccomyxa", MatchType: vlib.Exact,
		Results: []*vlib.ResultData{animal, strain, fuzzy, alga}})
	if !ok {
		t.Fatal("toMatch reported no match")
	}
	if m.RecordID != "g9" || m.MatchType != "Exact" {
		t.Errorf("top = %s %s, want g9 Exact", m.RecordID, m.MatchType)
	}
	if len(m.Homonyms) != 1 || m.Homonyms[0].RecordID != "g1" {
		t.Errorf("homonyms = %+v, want the alga genus g1 only", m.Homonyms)
	}
}

func TestScanJATSReadsAuthorships(t *testing.T) {
	jats := `<article xmlns:tp="http://www.plazi.org/taxpub" xmlns:hive="https://sfborg.org/hive">
<body>
<p><tp:taxon-name full-name="Navicula gottlandica"><italic>Navicula gottlandica</italic></tp:taxon-name> <tp:taxon-name full-name="Grunow">Grunow</tp:taxon-name> in Van Heurck 1880. The species is common.</p>
<p><tp:taxon-name full-name="Coccomyxa">Coccomyxa</tp:taxon-name> Schmidle, 1901 (Trebouxiophyceae) is a genus of algae.</p>
<p><tp:taxon-name full-name="Coccomyxa">Coccomyxa</tp:taxon-name> cells were counted.</p>
<p><tp:taxon-name full-name="Ulva rigida">Ulva rigida</tp:taxon-name> C.<hive:noise correction-id="c1"><!-- Running head --></hive:noise>Agardh 1823 and <tp:taxon-name full-name="Ulva lacinulata">U. lacinulata</tp:taxon-name> were found.</p>
<p><tp:taxon-name full-name="Amphidoma languida">Amphidoma languida</tp:taxon-name></p><p>Tillmann, Salas &amp; Elbrächter, 2012 is a citation in another paragraph.</p>
</body></article>`
	s, err := ScanJATS([]byte(jats))
	if err != nil {
		t.Fatalf("ScanJATS: %v", err)
	}
	wantNames := map[string]int{"Navicula gottlandica": 1, "Coccomyxa": 2, "Ulva rigida": 1, "Ulva lacinulata": 1, "Amphidoma languida": 1}
	if len(s.Names) != len(wantNames) {
		t.Errorf("Names = %v, want %v", s.Names, wantNames)
	}
	for k, v := range wantNames {
		if s.Names[k] != v {
			t.Errorf("Names[%q] = %d, want %d", k, s.Names[k], v)
		}
	}
	wantAuthors := map[string]string{
		"Navicula gottlandica": "Grunow in Van Heurck 1880.",
		"Coccomyxa":            "Schmidle, 1901",
		"Ulva rigida":          "C.Agardh 1823",
	}
	if len(s.Authors) != len(wantAuthors) {
		t.Errorf("Authors = %v, want %v", s.Authors, wantAuthors)
	}
	for name, auth := range wantAuthors {
		if s.Authors[name][auth] != 1 {
			t.Errorf("Authors[%q] = %v, want %q", name, s.Authors[name], auth)
		}
	}
}

func TestScanJATSUnparseableName(t *testing.T) {
	jats := `<p xmlns:tp="http://www.plazi.org/taxpub"><tp:taxon-name full-name="1234 ???">x</tp:taxon-name> Smith, 1900</p>`
	if _, err := ScanJATS([]byte(jats)); err != nil {
		t.Fatalf("ScanJATS: %v", err)
	}
}

func TestScanJATSNameListsAndBasionymAuthors(t *testing.T) {
	jats := `<article xmlns:tp="http://www.plazi.org/taxpub"><body>
<p><tp:taxon-name full-name="Grateloupia">Grateloupia</tp:taxon-name> C.Agardh, <tp:taxon-name full-name="Yonagunia">Yonagunia</tp:taxon-name> Kawag. &amp; Masuda, <tp:taxon-name full-name="Prionitis">Prionitis</tp:taxon-name> J.Agardh</p>
<p><tp:taxon-name full-name="Hantzschia amphioxys">Hantzschia amphioxys</tp:taxon-name> (Kützing) <tp:taxon-name full-name="Grunow">Grunow</tp:taxon-name> 1880 was found.</p>
</body></article>`
	s, err := ScanJATS([]byte(jats))
	if err != nil {
		t.Fatalf("ScanJATS: %v", err)
	}
	want := map[string]string{
		"Grateloupia":          "C.Agardh",
		"Yonagunia":            "Kawag. & Masuda",
		"Prionitis":            "J.Agardh",
		"Hantzschia amphioxys": "(Kützing) Grunow 1880",
	}
	for name, auth := range want {
		if s.Authors[name][auth] != 1 {
			t.Errorf("Authors[%q] = %v, want %q", name, s.Authors[name], auth)
		}
	}
	if _, ok := s.Names["Grunow"]; ok || len(s.Names) != 4 {
		t.Errorf("Names = %v, want the four names without Grunow", s.Names)
	}
}

func TestScanJATSAuthorWordInParentheses(t *testing.T) {
	jats := `<article xmlns:tp="http://www.plazi.org/taxpub"><body>
<p><tp:taxon-name full-name="Sellaphora seminulum">Sellaphora seminulum</tp:taxon-name> (<tp:taxon-name full-name="Grunow">Grunow</tp:taxon-name>) D.G. Mann 1989 (Figures 4).</p>
<p><tp:taxon-name full-name="E. frauenfeldii">E. frauenfeldii</tp:taxon-name> <tp:taxon-name full-name="Grunow">Grunow</tp:taxon-name>, and others.</p>
</body></article>`
	s, err := ScanJATS([]byte(jats))
	if err != nil {
		t.Fatalf("ScanJATS: %v", err)
	}
	if s.Authors["Sellaphora seminulum"]["(Grunow) D.G. Mann 1989"] != 1 {
		t.Errorf("Authors = %v", s.Authors)
	}
	if len(s.Names) != 1 || s.Names["Sellaphora seminulum"] != 1 {
		t.Errorf("Names = %v, want Sellaphora seminulum only", s.Names)
	}
}
