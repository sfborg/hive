package doi

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"10.1007/s13199-020-00711-x/":                      "10.1007/s13199-020-00711-x",
		" https://doi.org/10.1038/171737a0 ":               "10.1038/171737a0",
		"HTTPS://DX.DOI.ORG/10.1038/171737a0":              "10.1038/171737a0",
		"doi:10.1093/molbev/msab123.":                      "10.1093/molbev/msab123",
		"DOI:10.3897/phytokeys.212.89123),":                "10.3897/phytokeys.212.89123",
		"https://doi.org/10.1002%2F%28SICI%291097-4636":    "10.1002/(SICI)1097-4636",
		"10.1002/(SICI)1097-4636(199707)35:1<1::AID-JBM1>": "10.1002/(SICI)1097-4636(199707)35:1<1::AID-JBM1",
		"not a doi/": "not a doi/",
		"":           "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEqualIgnoresCaseAndForm(t *testing.T) {
	if !Equal("https://doi.org/10.1038/ABC", "10.1038/abc/") {
		t.Error("forms of the same DOI compare unequal")
	}
	if Equal("10.1038/abc", "10.1038/abd") {
		t.Error("different DOIs compare equal")
	}
}
