package view

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/sfborg/hive/core"
	"github.com/sfborg/sflib/pkg/coldp"
)

// createform.go owns the two-step name-add UX in the TUI:
//   step 0 — verbatim scientific name + code picker (rank is deferred
//            to the guess). "Save" (Ctrl+S) fires ParseNamePreview.
//   step 1 — atomized preview: every col__ field editable, rank picker
//            pre-selected from the guess. "Save" commits via
//            CreateName + CreateTaxon, respecting curator overrides.
//
// The state lives on detailModel (createStep + the extra pickers /
// inputs) but the initialization, key-routing, and rendering for
// step 1 live here to keep detail.go from ballooning.

// createPreviewField enumerates the atomized textinput fields shown on
// step 1. Ordered top-to-bottom so tab navigation walks the form the
// way the eye reads it.
//
// Both basionym_authorship + combination_authorship pairs live on the
// same Name record — for botany "Aus bus (L.) Smith" fills basionym=L.
// AND combination=Smith on the SAME row (CoLDP convention); zoology
// often leaves combination_* blank but the storage is identical. The
// terminal shows all four fields; a "next → add basionym" wizard
// (step 3c) can later spawn a separate name row when the basionym
// deserves its own record with a distinct reference.
//
// published_in_year is elided — the two atomized-year columns already
// capture the publication year and the reference-picker carries the
// bibliographic linkage.
const (
	cpfUninomial = iota
	cpfGenus
	cpfInfrageneric
	cpfSpecific
	cpfInfraspecific
	cpfCultivar
	cpfAuthorship
	cpfBasionymAuthor
	cpfBasionymYear
	cpfCombAuthor
	cpfCombYear
	cpfPublishedInPage
	cpfEtymology
	cpfRemarks
	cpfInputCount
)

// createPreviewPicker enumerates the combobox slots on step 1. Ordered
// after the textinputs in the tab cycle.
const (
	cppRank    = cpfInputCount + iota // rank picker (pre-filled from guess)
	cppStatus                         // NOMEN status picker
	cppRefID                          // reference picker
	cppFocusCount
)

var createPreviewLabels = [cpfInputCount]string{
	cpfUninomial:       "Uninomial",
	cpfGenus:           "Genus",
	cpfInfrageneric:    "Subgenus",
	cpfSpecific:        "Specific epithet",
	cpfInfraspecific:   "Infraspecific epithet",
	cpfCultivar:        "Cultivar epithet",
	cpfAuthorship:      "Authorship (verbatim)",
	cpfBasionymAuthor:  "Basionym author",
	cpfBasionymYear:    "Basionym year",
	cpfCombAuthor:      "Combination author",
	cpfCombYear:        "Combination year",
	cpfPublishedInPage: "Published in page",
	cpfEtymology:       "Etymology",
	cpfRemarks:         "Remarks",
}

// initPreviewInputs allocates the step-1 textinputs and pre-fills each
// from the parsed preview. Called after ParseNamePreview lands.
func (m *detailModel) initPreviewInputs(preview *coldp.Name) {
	for i := range m.createPreviewInputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 500
		m.createPreviewInputs[i] = ti
	}
	m.createPreviewInputs[cpfUninomial].SetValue(preview.Uninomial)
	m.createPreviewInputs[cpfGenus].SetValue(preview.Genus)
	m.createPreviewInputs[cpfInfrageneric].SetValue(preview.InfragenericEpithet)
	m.createPreviewInputs[cpfSpecific].SetValue(preview.SpecificEpithet)
	m.createPreviewInputs[cpfInfraspecific].SetValue(preview.InfraspecificEpithet)
	m.createPreviewInputs[cpfCultivar].SetValue(preview.CultivarEpithet)
	m.createPreviewInputs[cpfAuthorship].SetValue(preview.Authorship)
	m.createPreviewInputs[cpfBasionymAuthor].SetValue(preview.BasionymAuthorship)
	m.createPreviewInputs[cpfBasionymYear].SetValue(preview.BasionymAuthorshipYear)
	m.createPreviewInputs[cpfCombAuthor].SetValue(preview.CombinationAuthorship)
	m.createPreviewInputs[cpfCombYear].SetValue(preview.CombinationAuthorshipYear)
	m.createPreviewInputs[cpfPublishedInPage].SetValue(preview.PublishedInPage)
	m.createPreviewInputs[cpfEtymology].SetValue(preview.Etymology)
	m.createPreviewInputs[cpfRemarks].SetValue(preview.Remarks)

	// Rank picker: pre-select the guess. Fresh picker each entry so
	// stale search state doesn't leak from an earlier session.
	m.createRankPicker = newCombobox(vocabComboSource(m.vocab, "rank"), "rank…")
	if rid := preview.Rank.ID(); rid != "" {
		m.createRankPicker.SetValue(rid, vocabLabelFor(m.vocab, "rank", rid))
	}
	// NOMEN status picker filtered by the picked code (empty code =
	// all codes; nomenComboSource handles that).
	code := ""
	if m.createCodePicker.SelectedID() != "" {
		code = m.createCodePicker.SelectedID()
	}
	m.createStatusPicker = newCombobox(nomenComboSource(code), "nomenclatural status…")
	m.createRefPicker = newCombobox(referenceComboSource(m.a), "search references…")
}

// createPreviewFocusStep advances the preview-form's focus by delta
// (+1 for Tab, -1 for Shift+Tab) with wrap-around. Blurs the current
// input/picker before focusing the next.
func (m detailModel) createPreviewFocusStep(delta int) (detailModel, tea.Cmd) {
	// Blur current.
	m.blurPreviewFocus()
	m.createFocus = (m.createFocus + delta + cppFocusCount) % cppFocusCount
	return m, m.focusPreviewCurrent()
}

func (m *detailModel) blurPreviewFocus() {
	switch {
	case m.createFocus < cpfInputCount:
		m.createPreviewInputs[m.createFocus].Blur()
	case m.createFocus == cppRank:
		m.createRankPicker.Blur()
	case m.createFocus == cppStatus:
		m.createStatusPicker.Blur()
	case m.createFocus == cppRefID:
		m.createRefPicker.Blur()
	}
}

func (m *detailModel) focusPreviewCurrent() tea.Cmd {
	switch {
	case m.createFocus < cpfInputCount:
		return m.createPreviewInputs[m.createFocus].Focus()
	case m.createFocus == cppRank:
		return m.createRankPicker.Focus()
	case m.createFocus == cppStatus:
		return m.createStatusPicker.Focus()
	case m.createFocus == cppRefID:
		return m.createRefPicker.Focus()
	}
	return nil
}

// updateCreatePreview routes a key to the focused input/picker on
// step 1. Tab / Shift+Tab cycle focus; everything else goes to the
// focused widget.
func (m detailModel) updateCreatePreview(msg tea.KeyMsg) (detailModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		return m.createPreviewFocusStep(1)
	case "shift+tab":
		return m.createPreviewFocusStep(-1)
	}
	if m.createFocus < cpfInputCount {
		var cmd tea.Cmd
		m.createPreviewInputs[m.createFocus], cmd = m.createPreviewInputs[m.createFocus].Update(msg)
		return m, cmd
	}
	var picker *combobox
	switch m.createFocus {
	case cppRank:
		picker = &m.createRankPicker
	case cppStatus:
		picker = &m.createStatusPicker
	case cppRefID:
		picker = &m.createRefPicker
	}
	if picker != nil {
		newP, cmd := picker.Update(msg)
		*picker = newP
		return m, cmd
	}
	return m, nil
}

// parsePreviewMsg carries a ParseNamePreview result back to the model
// so the transition to step 1 happens on the tea event loop.
type parsePreviewMsg struct {
	preview *coldp.Name
	err     error
}

// parsePreviewCmd runs the archive-side parse in a goroutine. gnparser
// itself is CPU-only so no ctx-honoring is needed today; the goroutine
// gate is enough to keep the tea event loop responsive.
func (m *detailModel) parsePreviewCmd(verbatim, code string) tea.Cmd {
	a := m.a
	return func() tea.Msg {
		return parsePreviewMsg{preview: a.ParseNamePreview(code, verbatim)}
	}
}

// composeCreateName builds the coldp.Name to hand to CreateName from
// the current preview inputs. Empty fields survive as "" so CreateName's
// fill-from-parse can re-derive them (safety net if the curator cleared
// a field expecting the parser to fill it back in). Rank/Status go
// through the empty-safe helpers to avoid coercing empty into a zero
// enum value.
//
// Both authorship pairs (basionym + combination) live on this record
// per CoLDP convention — a botanical "Aus bus (L.) Smith" gets basionym
// author L. AND combination author Smith on the same row. published_in_year
// is derived from whichever year field the curator populated (combination
// takes precedence when both are set — it's the year of the current
// combination's publication).
func (m detailModel) composeCreateName(verbatim, code string) coldp.Name {
	basYear := m.createPreviewInputs[cpfBasionymYear].Value()
	combYear := m.createPreviewInputs[cpfCombYear].Value()
	pubYear := combYear
	if pubYear == "" {
		pubYear = basYear
	}
	return coldp.Name{
		ScientificName:            verbatim,
		ScientificNameString:      verbatim,
		Authorship:                m.createPreviewInputs[cpfAuthorship].Value(),
		Rank:                      core.ParseRank(m.createRankPicker.SelectedID()),
		Code:                      nomcodeNew(code),
		Uninomial:                 m.createPreviewInputs[cpfUninomial].Value(),
		Genus:                     m.createPreviewInputs[cpfGenus].Value(),
		InfragenericEpithet:       m.createPreviewInputs[cpfInfrageneric].Value(),
		SpecificEpithet:           m.createPreviewInputs[cpfSpecific].Value(),
		InfraspecificEpithet:      m.createPreviewInputs[cpfInfraspecific].Value(),
		CultivarEpithet:           m.createPreviewInputs[cpfCultivar].Value(),
		BasionymAuthorship:        m.createPreviewInputs[cpfBasionymAuthor].Value(),
		BasionymAuthorshipYear:    basYear,
		CombinationAuthorship:     m.createPreviewInputs[cpfCombAuthor].Value(),
		CombinationAuthorshipYear: combYear,
		Status:                    core.ParseNomStatus(m.createStatusPicker.SelectedID()),
		ReferenceID:               m.createRefPicker.SelectedID(),
		PublishedInYear:           pubYear,
		PublishedInPage:           m.createPreviewInputs[cpfPublishedInPage].Value(),
		Etymology:                 m.createPreviewInputs[cpfEtymology].Value(),
		Remarks:                   m.createPreviewInputs[cpfRemarks].Value(),
	}
}

func nomcodeNew(code string) nomcode.Code { return nomcode.New(code) }

// renderCreatePreview draws the step-1 atomized preview form. Groups
// fields into three sections (atomized, authorship, publication) with
// the rank picker at the top so the curator can flip the guess before
// scanning the rest of the form.
func (m detailModel) renderCreatePreview() string {
	var b strings.Builder
	parent := m.createParentName
	if parent == "" {
		parent = "(root)"
	}
	header := headerStyle.Render("New taxon under ") + parent +
		"  " + dimStyle.Render("(step 2 / 2 — atomized preview)")
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", 40)))
	b.WriteString("\n\n")

	// Verbatim recap for context.
	b.WriteString(labelStyle.Render("Verbatim: "))
	b.WriteString(m.createSciName.Value())
	b.WriteString("\n\n")

	// Rank picker (focused first-ish so the pre-selected guess is
	// prominent; still tab-cycled with the rest).
	b.WriteString(m.renderPreviewPickerRow("Rank", &m.createRankPicker))

	b.WriteString(sectionHeaderStyle.Render("── atomized ──"))
	b.WriteByte('\n')
	for _, f := range []int{cpfUninomial, cpfGenus, cpfInfrageneric,
		cpfSpecific, cpfInfraspecific, cpfCultivar} {
		b.WriteString(m.renderPreviewInputRow(f))
	}

	b.WriteString(sectionHeaderStyle.Render("── authorship ──"))
	b.WriteByte('\n')
	b.WriteString(m.renderPreviewInputRow(cpfAuthorship))
	b.WriteString(sectionHeaderStyle.Render("── basionym (original) ──"))
	b.WriteByte('\n')
	for _, f := range []int{cpfBasionymAuthor, cpfBasionymYear} {
		b.WriteString(m.renderPreviewInputRow(f))
	}
	b.WriteString(sectionHeaderStyle.Render("── combination (current) ──"))
	b.WriteByte('\n')
	for _, f := range []int{cpfCombAuthor, cpfCombYear} {
		b.WriteString(m.renderPreviewInputRow(f))
	}

	b.WriteString(sectionHeaderStyle.Render("── publication ──"))
	b.WriteByte('\n')
	b.WriteString(m.renderPreviewPickerRow(m.previewRefLabel(), &m.createRefPicker))
	b.WriteString(m.renderPreviewInputRow(cpfPublishedInPage))

	b.WriteString(sectionHeaderStyle.Render("── metadata ──"))
	b.WriteByte('\n')
	b.WriteString(m.renderPreviewPickerRow("Nom status", &m.createStatusPicker))
	for _, f := range []int{cpfEtymology, cpfRemarks} {
		b.WriteString(m.renderPreviewInputRow(f))
	}

	b.WriteByte('\n')
	if m.saveError != "" {
		b.WriteString(errStyle.Render(m.saveError))
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	if m.saving {
		b.WriteString(dimStyle.Render("creating…"))
	} else {
		b.WriteString(dimStyle.Render(
			"[tab] next   [shift+tab] prev   [ctrl+s] create   [esc] back to verbatim"))
	}
	return b.String()
}

// previewRefLabel names the reference picker after whatever atomized
// authorship fields the curator has filled in, so it's hard to mistake
// which combination the reference is being attached to. Mirrors the
// PWA's referenceLabelFor rule.
func (m detailModel) previewRefLabel() string {
	combA := strings.TrimSpace(m.createPreviewInputs[cpfCombAuthor].Value())
	combY := strings.TrimSpace(m.createPreviewInputs[cpfCombYear].Value())
	basA := strings.TrimSpace(m.createPreviewInputs[cpfBasionymAuthor].Value())
	basY := strings.TrimSpace(m.createPreviewInputs[cpfBasionymYear].Value())
	fmtPair := func(a, y string) string {
		switch {
		case a != "" && y != "":
			return a + ", " + y
		case a != "":
			return a
		}
		return y
	}
	if combA != "" || combY != "" {
		who := fmtPair(combA, combY)
		if who != "" {
			return "Reference for the combination (" + who + ")"
		}
		return "Reference for the combination"
	}
	if basA != "" || basY != "" {
		who := fmtPair(basA, basY)
		if who != "" {
			return "Reference (basionym: " + who + ")"
		}
		return "Reference"
	}
	return "Reference"
}

func (m detailModel) renderPreviewInputRow(f int) string {
	label := labelStyle.Render(fmt.Sprintf("%-24s", createPreviewLabels[f]+":"))
	return label + " " + m.createPreviewInputs[f].View() + "\n"
}

func (m detailModel) renderPreviewPickerRow(name string, p *combobox) string {
	label := labelStyle.Render(fmt.Sprintf("%-24s", name+":"))
	return label + " " + p.View() + "\n"
}

