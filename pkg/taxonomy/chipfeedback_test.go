package taxonomy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sfborg/hive/pkg/correction"
)

// Two chips share the text "Blixaea quinquecornus" so the tests can
// pin down occurrence handling; the third has no gn:source, standing
// in for a chip GNverifier left unstamped.
const chipJATS = `<body>
<p>First <tp:taxon-name full-name="Blixaea quinquecornus" gn:source="verified" gn:score="0.941">Blixaea quinquecornus</tp:taxon-name> here.</p>
<p>Then <tp:taxon-name full-name="Durinskia baltica" gn:source="ambiguous">Durinskia
baltica</tp:taxon-name> and <tp:taxon-name full-name="Wettst." >Wettst.</tp:taxon-name>.</p>
<p>Again <tp:taxon-name full-name="Blixaea quinquecornus" gn:source="verified">Blixaea quinquecornus</tp:taxon-name>.</p>
</body>`

func chipCorrection(t *testing.T, p correction.ChipFeedbackPayload) *correction.Correction {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &correction.Correction{
		ID:      "c_test",
		Kind:    correction.KindChipFeedback,
		Payload: string(raw),
	}
}

func TestApplyChipFeedbackReject(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action: correction.ChipActionReject,
		Text:   "Wettst.",
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := string(out)
	if strings.Contains(got, `full-name="Wettst."`) {
		t.Errorf("chip markup still present:\n%s", got)
	}
	if !strings.Contains(got, "and Wettst..") {
		t.Errorf("chip text was not left in place:\n%s", got)
	}
	if strings.Count(got, "<tp:taxon-name") != 3 {
		t.Errorf("expected the other three chips to survive, got %d", strings.Count(got, "<tp:taxon-name"))
	}
}

func TestApplyChipFeedbackRejectSecondOccurrence(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action:     correction.ChipActionReject,
		Text:       "Blixaea quinquecornus",
		Occurrence: 2,
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<p>First <tp:taxon-name full-name="Blixaea quinquecornus"`) {
		t.Errorf("first occurrence should be untouched:\n%s", got)
	}
	if !strings.Contains(got, "<p>Again Blixaea quinquecornus.</p>") {
		t.Errorf("second occurrence should be unwrapped:\n%s", got)
	}
}

func TestApplyChipFeedbackReclassify(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action:          correction.ChipActionReclassify,
		Text:            "Blixaea quinquecornus",
		OriginalSource:  "verified",
		CorrectedSource: "ambiguous",
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `full-name="Blixaea quinquecornus" gn:source="ambiguous" gn:score="0.941"`) {
		t.Errorf("gn:source was not rewritten in place:\n%s", got)
	}
	if strings.Count(got, `gn:source="verified"`) != 1 {
		t.Errorf("only the first chip should change; got %d verified chips", strings.Count(got, `gn:source="verified"`))
	}
}

func TestApplyChipFeedbackReclassifyAddsMissingAttribute(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action:          correction.ChipActionReclassify,
		Text:            "Wettst.",
		CorrectedSource: "unresolved",
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(string(out), `full-name="Wettst." gn:source="unresolved">`) {
		t.Errorf("gn:source was not appended:\n%s", out)
	}
}

func TestApplyChipFeedbackEditFullName(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action:            correction.ChipActionEdit,
		Text:              "Durinskia baltica",
		OriginalFullName:  "Durinskia baltica",
		CorrectedFullName: `Durinskia "baltica" & al.`,
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(string(out), `full-name="Durinskia &quot;baltica&quot; &amp; al." gn:source="ambiguous"`) {
		t.Errorf("full-name was not rewritten with escaping:\n%s", out)
	}
}

// Chip text wrapped across lines still matches, since the comparison
// collapses whitespace.
func TestApplyChipFeedbackMatchesAcrossLineWrap(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action: correction.ChipActionReject,
		Text:   "Durinskia baltica",
	})
	out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if strings.Contains(string(out), `full-name="Durinskia baltica"`) {
		t.Errorf("wrapped chip text did not match:\n%s", out)
	}
}

func TestApplyChipFeedbackNoMatchLeavesJATSUnchanged(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload correction.ChipFeedbackPayload
	}{
		{"unknown text", correction.ChipFeedbackPayload{Action: correction.ChipActionReject, Text: "Nonexistent name"}},
		{"occurrence past the end", correction.ChipFeedbackPayload{
			Action: correction.ChipActionReject, Text: "Blixaea quinquecornus", Occurrence: 3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := ApplyChipFeedbackToJATS([]byte(chipJATS), chipCorrection(t, tt.payload))
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if string(out) != chipJATS {
				t.Errorf("JATS changed:\n%s", out)
			}
		})
	}
}

func TestApplyChipFeedbackErrors(t *testing.T) {
	tests := []struct {
		name string
		c    *correction.Correction
		want string
	}{
		{"wrong kind", &correction.Correction{Kind: correction.KindTextRemove, Payload: `{"action":"reject","text":"x"}`},
			"expected chip-feedback kind"},
		{"empty payload", &correction.Correction{Kind: correction.KindChipFeedback, Payload: ""}, "payload required"},
		{"bad JSON", &correction.Correction{Kind: correction.KindChipFeedback, Payload: "{"}, "parse payload"},
		{"no text", chipCorrection(t, correction.ChipFeedbackPayload{Action: correction.ChipActionReject}), "payload.text required"},
		{"unknown action", chipCorrection(t, correction.ChipFeedbackPayload{Action: "delete", Text: "Wettst."}), "unknown action"},
		{"reclassify without source", chipCorrection(t, correction.ChipFeedbackPayload{
			Action: correction.ChipActionReclassify, Text: "Wettst."}), "needs corrected_source"},
		{"edit without full name", chipCorrection(t, correction.ChipFeedbackPayload{
			Action: correction.ChipActionEdit, Text: "Wettst."}), "needs corrected_full_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyChipFeedbackToJATS([]byte(chipJATS), tt.c)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// A reject applied twice is a no-op the second time, which is what a
// re-apply pass over active corrections depends on.
func TestApplyChipFeedbackRejectIsIdempotent(t *testing.T) {
	c := chipCorrection(t, correction.ChipFeedbackPayload{
		Action: correction.ChipActionReject,
		Text:   "Wettst.",
	})
	once, err := ApplyChipFeedbackToJATS([]byte(chipJATS), c)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	twice, err := ApplyChipFeedbackToJATS(once, c)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if string(twice) != string(once) {
		t.Errorf("second apply changed the JATS:\n%s", twice)
	}
}
