package refmanager

import (
	"testing"
)

// TestFindMissingFigTableLabels exercises the pure body-vs-caption
// diff on hand-built JATS fragments. Covers the shapes we observe
// in real GROBID output: xref citations, unwrapped "Figure N"
// prose, and the caption-self-reference case that must not be
// double-counted as a citation of the fig itself.
func TestFindMissingFigTableLabels(t *testing.T) {
	tests := []struct {
		name string
		jats string
		want []string
	}{
		{
			name: "all cited figures present",
			jats: `<article><body>
<p>See <xref rid="fig_0">Figure 1</xref> and figure 2.</p>
<fig id="fig_0"><caption><p>Figure 1. First fig.</p></caption></fig>
<fig id="fig_1"><caption><p>Figure 2. Second fig.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "cited but missing figure",
			jats: `<article><body>
<p>See figure 1 and figure 3.</p>
<fig id="fig_0"><caption><p>Figure 1. Present.</p></caption></fig>
</body></article>`,
			want: []string{"Figure 3"},
		},
		{
			name: "cited but missing table",
			jats: `<article><body>
<p>Data in table 1 and table 2.</p>
<table-wrap id="tab_0"><caption><p>Table 1. Present.</p></caption></table-wrap>
</body></article>`,
			want: []string{"Table 2"},
		},
		{
			name: "sub-panel xref counts as parent figure",
			jats: `<article><body>
<p>See <xref rid="fig_0">1 A1</xref>.</p>
<fig id="fig_0"><caption><p>Figure 1. Parent.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "caption self-reference not counted",
			jats: `<article><body>
<fig id="fig_2"><caption><p>Figure 3. Third fig described.</p></caption></fig>
</body></article>`,
			want: nil,
		},
		{
			name: "multiple missing sorted",
			jats: `<article><body>
<p>Figure 5 and table 3 and figure 4.</p>
</body></article>`,
			want: []string{"Figure 4", "Figure 5", "Table 3"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findMissingFigTableLabels([]byte(tc.jats))
			if !stringSliceEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
