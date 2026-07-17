package view

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/sfborg/hive/core"
	"github.com/sfborg/sflib/pkg/coldp"
)

// Editable form fields, in tab order. Fields split into two aggregate
// sections: the first block writes back to the taxon row via UpdateTaxon
// (plus a separate MoveTaxon call when Parent changes), the second block
// writes back to the associated name row via UpdateName. All writes go
// through a single WithTx for atomicity — a curator changing scientific
// name + remarks + parent in one save gets an all-or-nothing commit.
//
// Field kinds:
//   * picker  — combobox (Parent, Rank, Code, NomStatus)
//   * cycler  — tri-state (Extinct)
//   * text    — everything else (textinput.Model in m.inputs)
const (
	// Taxon fields
	fieldParent = iota // combobox — server-search source
	fieldNamePhrase
	fieldScrutinizer
	fieldScrutinizerID
	fieldScrutinizerDate
	fieldExtinct // cycler — space cycles unset/yes/no
	fieldLink
	fieldTaxonRemarks

	// Name fields — hidden and skipped in tab order when the taxon has
	// no associated name (rare, but possible for legacy archives).
	// Layout mirrors the create pane's atomized preview: verbatim
	// scientific name + rank + code + verbatim authorship at the top,
	// then the atomized name block, then the two authorship pairs
	// (basionym + combination) — CoLDP puts both on the same Name row
	// so both are legitimately editable together.
	fieldScientificNameString
	fieldRank             // combobox — vocab source
	fieldCode             // combobox — vocab source
	fieldAuthorship       // verbatim authorship (unatomized)
	fieldUninomial        // atomized name — same set as the create pane
	fieldGenus            //
	fieldInfrageneric     // subgenus
	fieldSpecific         //
	fieldInfraspecific    //
	fieldCultivar         //
	fieldBasionymAuthor   // atomized authorship — basionym (original)
	fieldBasionymYear     //
	fieldCombAuthor       // atomized authorship — combination (current)
	fieldCombYear         //
	fieldNomStatus        // combobox — vocab source
	fieldReferenceID      // combobox — reference search source
	fieldPublishedInPage  //
	fieldEtymology
	fieldNameRemarks

	fieldCount
)

// firstNameField is the boundary between taxon and name inputs — used for
// section rendering and to detect "no name to edit" when skipping.
const firstNameField = fieldScientificNameString

var fieldLabels = [fieldCount]string{
	fieldParent:               "Parent",
	fieldNamePhrase:           "Name phrase",
	fieldScrutinizer:          "Scrutinizer",
	fieldScrutinizerID:        "Scrutinizer ID",
	fieldScrutinizerDate:      "Scrutinizer date",
	fieldExtinct:              "Extinct",
	fieldLink:                 "Link",
	fieldTaxonRemarks:         "Taxon remarks",
	fieldScientificNameString: "Scientific name",
	fieldRank:                 "Rank",
	fieldCode:                 "Code",
	fieldAuthorship:           "Verbatim authorship",
	fieldUninomial:            "Uninomial",
	fieldGenus:                "Genus",
	fieldInfrageneric:         "Subgenus",
	fieldSpecific:             "Specific epithet",
	fieldInfraspecific:        "Infraspecific epithet",
	fieldCultivar:             "Cultivar epithet",
	fieldBasionymAuthor:       "Basionym author",
	fieldBasionymYear:         "Basionym year",
	fieldCombAuthor:           "Combination author",
	fieldCombYear:             "Combination year",
	fieldNomStatus:            "Nom status",
	fieldReferenceID:          "Reference",
	fieldPublishedInPage:      "Published in page",
	fieldEtymology:            "Etymology",
	fieldNameRemarks:          "Name remarks",
}

// fieldIsPicker reports whether the field uses a combobox widget
// (as opposed to a plain textinput or the tri-state cycler).
func fieldIsPicker(f int) bool {
	return f == fieldParent || f == fieldRank || f == fieldCode ||
		f == fieldNomStatus || f == fieldReferenceID
}


// detailModel is the right pane — highlighted taxon detail plus, when
// `editable` is on, an in-pane edit form. Feature parity with the PWA
// edit form: same field set, same tri-state extinct handling, same
// optimistic-concurrency semantics via col__modified as If-Match.
type detailModel struct {
	a             *core.Archive
	editable      bool
	actor         string
	current       string // taxon ID currently displayed
	taxon         *coldp.Taxon
	name          *coldp.Name // fetched only when the taxon's NameID is non-empty
	parentLabel   string      // resolved display for taxon.ParentID (empty at root)
	width, height int
	err           error
	loading       bool

	// Edit-mode state
	editing        bool
	inputs         [fieldCount]textinput.Model
	focusedField   int
	extinctState   int // 0=unset, 1=yes, 2=no
	saving         bool
	saveError      string
	taxonIfMatch   string // col__modified on the taxon at edit-mode entry
	nameIfMatch    string // col__modified on the name at edit-mode entry
	nameEditable   bool   // false when taxon has no NameID (name fields hidden)
	taxonOriginals taxonFieldSnapshot
	nameOriginals  nameFieldSnapshot

	// Vocabulary bundle, lazy-loaded on first EnterEditMode and shared
	// across the vocab-backed comboboxes below.
	vocab    *core.Vocabulary
	vocabErr error

	// Combobox instances — one per picker field. Populated in
	// EnterEditMode. Reset (not reallocated) on subsequent entries so
	// their debounce / lastQuery state doesn't leak across sessions.
	parentPicker    combobox
	rankPicker      combobox
	codePicker      combobox
	statusPicker    combobox
	referencePicker combobox

	// Add-reference modal. Non-nil ⇒ modal is active; the shell routes
	// all key input to it via detailModel.Update while it lives.
	addRef *addRefModel

	// Parent-picker specifics. The originalParent snapshot enables a
	// dirty check for the move step; move is a separate core operation
	// from UpdateTaxon so we track it independently of taxonOriginals.
	parentOriginalID string

	// Create-mode state. Independent of editing above — the two never
	// overlap. Two-step flow:
	//   createStep 0 — verbatim scientific name + code picker; Save
	//                  fires ParseNamePreview and advances to step 1.
	//   createStep 1 — atomized preview: rank picker + textinputs for
	//                  every col__ field; Save commits.
	creating           bool
	createStep         int
	createParentID     string // "" → root-level taxon
	createParentName   string // display label for the header
	createSciName      textinput.Model
	createRankPicker   combobox
	createCodePicker   combobox
	createStatusPicker combobox
	createRefPicker    combobox
	createFocus        int // step 0: 0=sci, 1=code; step 1: cpf* / cpp* enum
	// Preview textinputs — one per atomized col__ field on step 1.
	// Populated by initPreviewInputs after the parse lands.
	createPreviewInputs [cpfInputCount]textinput.Model
}

// taxonFieldSnapshot captures the pre-edit values of taxon-writable fields
// so Save can detect whether any of them changed. Sparing UpdateTaxon when
// only name fields moved avoids bumping the taxon's col__modified.
type taxonFieldSnapshot struct {
	NamePhrase      string
	Scrutinizer     string
	ScrutinizerID   string
	ScrutinizerDate string
	Link            string
	Remarks         string
	Extinct         sql.NullBool
}

// nameFieldSnapshot is the analogous pre-edit snapshot for name fields.
type nameFieldSnapshot struct {
	ScientificNameString      string
	Authorship                string
	RankID                    string
	CodeID                    string
	StatusID                  string
	ReferenceID               string
	Uninomial                 string
	Genus                     string
	InfragenericEpithet       string
	SpecificEpithet           string
	InfraspecificEpithet      string
	CultivarEpithet           string
	BasionymAuthorship        string
	BasionymAuthorshipYear    string
	CombinationAuthorship     string
	CombinationAuthorshipYear string
	PublishedInPage           string
	Etymology                 string
	Remarks                   string
}

func newDetailModel(a *core.Archive, editable bool, actor string) detailModel {
	m := detailModel{a: a, editable: editable, actor: actor}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 2000
		m.inputs[i] = ti
	}
	return m
}

// detailLoadedMsg is delivered when a taxon+name pair has been fetched.
// parentLabel is the human-readable label for the parent taxon (empty
// when the taxon is at root) so the view pane doesn't render a raw UUID.
type detailLoadedMsg struct {
	taxonID     string
	taxon       *coldp.Taxon
	name        *coldp.Name
	parentLabel string
	err         error
}

// savedMsg is delivered when a WithTx save round-trip completes. `name`
// is populated only when the name aggregate was actually updated — a
// nil `name` means "keep whatever the pane already has."
// parentMoved is true when Save issued a MoveTaxon call — the shell uses
// it to reload the tree along the taxon's new ancestor chain so the
// reparented row appears in its new location.
type savedMsg struct {
	taxon       *coldp.Taxon
	name        *coldp.Name
	parentLabel string
	parentMoved bool
	err         error
}

// parentResolvedMsg delivers the display name for a taxon id — used
// during EnterEditMode to fill the parent picker with a human label
// instead of a raw UUID. If the lookup fails, name is empty and the
// picker falls back to showing the id.
type parentResolvedMsg struct {
	id   string
	name string
}

// pickerFor returns a pointer to the combobox for the given field, or
// nil if the field isn't a picker. Pointer return lets callers mutate
// the picker in place — combobox values contain textinput.Model which
// is itself a struct, and we want key handling to affect the actual
// instance in the model, not a copy.
func (m *detailModel) pickerFor(f int) *combobox {
	switch f {
	case fieldParent:
		return &m.parentPicker
	case fieldRank:
		return &m.rankPicker
	case fieldCode:
		return &m.codePicker
	case fieldNomStatus:
		return &m.statusPicker
	case fieldReferenceID:
		return &m.referencePicker
	}
	return nil
}

// resolveParentName fires a background lookup for the parent taxon's
// display name via core.TaxonRef (same server-side formatting the PWA
// uses). Result arrives as a parentResolvedMsg which the Update method
// plumbs into the parentPicker's committed name field.
func (m *detailModel) resolveParentName(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	a := m.a
	return func() tea.Msg {
		ctx := context.Background()
		ref, err := a.TaxonRef(ctx, id)
		if err != nil {
			return parentResolvedMsg{id: id}
		}
		return parentResolvedMsg{id: id, name: ref.Label.Text}
	}
}

// Load starts an async fetch for the given taxon ID. Fetches parent label
// in the same pass so the detail pane can render a human-readable parent
// (matches the /api/taxon response, which server-side joins the parent).
func (m detailModel) Load(taxonID string) tea.Cmd {
	if taxonID == "" {
		return nil
	}
	a := m.a
	return func() tea.Msg {
		ctx := context.Background()
		t, err := a.GetTaxon(ctx, taxonID)
		if err != nil {
			return detailLoadedMsg{taxonID: taxonID, err: err}
		}
		var n *coldp.Name
		if t.NameID != "" {
			nameRes, nErr := a.GetName(ctx, t.NameID)
			if nErr != nil {
				return detailLoadedMsg{taxonID: taxonID, taxon: t}
			}
			n = nameRes
		}
		var parentLabel string
		if t.ParentID != "" {
			if ref, refErr := a.TaxonRef(ctx, t.ParentID); refErr == nil {
				parentLabel = ref.Label.Text
			}
		}
		return detailLoadedMsg{taxonID: taxonID, taxon: t, name: n, parentLabel: parentLabel}
	}
}

// Editing reports whether the pane is in edit mode. The root model checks
// this to decide whether keystrokes go to form inputs vs. tree navigation.
func (m detailModel) Editing() bool { return m.editing }

// AddingReference reports whether the add-reference modal is open. The
// shell checks this before dispatching to the edit-form key handler so
// the modal owns all input while it's active.
func (m detailModel) AddingReference() bool { return m.addRef != nil }

// OpenAddReference constructs the modal seeded with the current name's
// context (canonical + authors + year for the BHLnames tab) and returns
// the modal's startup cmd. No-op when no name is attached — the modal
// can't do meaningful work without a scientific name.
func (m *detailModel) OpenAddReference() tea.Cmd {
	if m.name == nil {
		return nil
	}
	canonical := m.name.CanonicalSimple
	if canonical == "" {
		canonical = m.name.ScientificName
	}
	year := 0
	if y := m.name.PublishedInYear; y != "" {
		// Best-effort parse; PublishedInYear is a free-form string
		// (may be "1758", "1758-59", "1758?"). Fall back to 0 on
		// anything the strict parse can't handle.
		fmt.Sscanf(y, "%d", &year)
	}
	sub := newAddRefModel(m.a, m.actor, canonical, m.name.Authors, year)
	m.addRef = &sub
	return m.addRef.Init()
}

// CloseAddReference discards the modal without applying a pick — used
// when the shell traps Esc and wants to force-close.
func (m *detailModel) CloseAddReference() {
	m.addRef = nil
}

// CanEdit reports whether entering edit mode is legal — requires editable
// mode and a loaded taxon.
func (m detailModel) CanEdit() bool {
	return m.editable && m.taxon != nil && !m.editing
}

// EnterEditMode seeds the form from the current taxon (and name, if any)
// and takes edit-mode focus. Returns false if editing is not permitted.
// The bool return is retained for API compat; a real caller wanting the
// initial-focus command should call EnterEditModeCmd instead.
func (m *detailModel) EnterEditMode() bool {
	_, ok := m.EnterEditModeCmd()
	return ok
}

// EnterEditModeCmd is the cmd-returning form-init: same as EnterEditMode
// but also returns any tea.Cmd needed to kick off async initialization
// (parent-name resolution, initial vocab-picker search). Callers that
// route the returned cmd through tea's runtime get a smoother experience;
// callers using EnterEditMode() drop the cmd and lose those niceties.
func (m *detailModel) EnterEditModeCmd() (tea.Cmd, bool) {
	if !m.CanEdit() {
		return nil, false
	}
	// Load the vocabulary bundle on first entry. Cached on Archive, so
	// re-entering edit mode is free.
	if m.vocab == nil && m.vocabErr == nil {
		m.vocab, m.vocabErr = m.a.Vocabulary(context.Background())
	}
	m.editing = true
	m.saveError = ""

	// Taxon fields
	m.taxonIfMatch = m.taxon.Modified
	m.inputs[fieldNamePhrase].SetValue(m.taxon.NamePhrase)
	m.inputs[fieldScrutinizer].SetValue(m.taxon.Scrutinizer)
	m.inputs[fieldScrutinizerID].SetValue(m.taxon.ScrutinizerID)
	m.inputs[fieldScrutinizerDate].SetValue(m.taxon.ScrutinizerDate)
	m.inputs[fieldLink].SetValue(m.taxon.Link)
	m.inputs[fieldTaxonRemarks].SetValue(m.taxon.Remarks)
	switch {
	case !m.taxon.Extinct.Valid:
		m.extinctState = 0
	case m.taxon.Extinct.Bool:
		m.extinctState = 1
	default:
		m.extinctState = 2
	}
	m.taxonOriginals = taxonFieldSnapshot{
		NamePhrase:      m.taxon.NamePhrase,
		Scrutinizer:     m.taxon.Scrutinizer,
		ScrutinizerID:   m.taxon.ScrutinizerID,
		ScrutinizerDate: m.taxon.ScrutinizerDate,
		Link:            m.taxon.Link,
		Remarks:         m.taxon.Remarks,
		Extinct:         m.taxon.Extinct,
	}

	// Name fields — only when a name is attached. Legacy archives may have
	// a taxon with an empty NameID; in that case we hide the name section
	// entirely and Save writes just the taxon.
	m.nameEditable = m.name != nil
	if m.nameEditable {
		m.nameIfMatch = m.name.Modified
		m.inputs[fieldScientificNameString].SetValue(m.name.ScientificNameString)
		m.inputs[fieldAuthorship].SetValue(m.name.Authorship)
		m.inputs[fieldUninomial].SetValue(m.name.Uninomial)
		m.inputs[fieldGenus].SetValue(m.name.Genus)
		m.inputs[fieldInfrageneric].SetValue(m.name.InfragenericEpithet)
		m.inputs[fieldSpecific].SetValue(m.name.SpecificEpithet)
		m.inputs[fieldInfraspecific].SetValue(m.name.InfraspecificEpithet)
		m.inputs[fieldCultivar].SetValue(m.name.CultivarEpithet)
		m.inputs[fieldBasionymAuthor].SetValue(m.name.BasionymAuthorship)
		m.inputs[fieldBasionymYear].SetValue(m.name.BasionymAuthorshipYear)
		m.inputs[fieldCombAuthor].SetValue(m.name.CombinationAuthorship)
		m.inputs[fieldCombYear].SetValue(m.name.CombinationAuthorshipYear)
		m.inputs[fieldPublishedInPage].SetValue(m.name.PublishedInPage)
		m.inputs[fieldEtymology].SetValue(m.name.Etymology)
		m.inputs[fieldNameRemarks].SetValue(m.name.Remarks)
		m.nameOriginals = nameFieldSnapshot{
			ScientificNameString:      m.name.ScientificNameString,
			Authorship:                m.name.Authorship,
			RankID:                    m.name.Rank.ID(),
			CodeID:                    m.name.Code.ID(),
			StatusID:                  core.NameRawStatus(m.name.ID),
			ReferenceID:               core.PrimaryReferenceID(m.name.ReferenceID),
			Uninomial:                 m.name.Uninomial,
			Genus:                     m.name.Genus,
			InfragenericEpithet:       m.name.InfragenericEpithet,
			SpecificEpithet:           m.name.SpecificEpithet,
			InfraspecificEpithet:      m.name.InfraspecificEpithet,
			CultivarEpithet:           m.name.CultivarEpithet,
			BasionymAuthorship:        m.name.BasionymAuthorship,
			BasionymAuthorshipYear:    m.name.BasionymAuthorshipYear,
			CombinationAuthorship:     m.name.CombinationAuthorship,
			CombinationAuthorshipYear: m.name.CombinationAuthorshipYear,
			PublishedInPage:           m.name.PublishedInPage,
			Etymology:                 m.name.Etymology,
			Remarks:                   m.name.Remarks,
		}
	}

	// Combobox instances — fresh each entry so no stale results / hover
	// state leaks between edits.
	m.parentPicker = newCombobox(taxonComboSource(m.a), "type to search taxa…")
	m.parentOriginalID = m.taxon.ParentID
	// Seed with the id first; the parentResolvedMsg cmd (returned below)
	// fills in the display name asynchronously.
	m.parentPicker.SetValue(m.taxon.ParentID, m.taxon.ParentID)

	m.rankPicker = newCombobox(vocabComboSource(m.vocab, "rank"), "rank…")
	m.codePicker = newCombobox(vocabComboSource(m.vocab, "nom_code"), "nomenclatural code…")
	// The status picker sources from NOMEN, filtered by the current
	// name's nom_code. Rebuilds if the curator switches code in-form —
	// see updateEdit's code-picker hook.
	nameCode := ""
	if m.nameEditable {
		nameCode = m.name.Code.ID()
	}
	m.statusPicker = newCombobox(nomenComboSource(nameCode), "nomenclatural status…")
	m.referencePicker = newCombobox(referenceComboSource(m.a), "search references…")
	if m.nameEditable {
		m.rankPicker.SetValue(m.name.Rank.ID(), vocabLabelFor(m.vocab, "rank", m.name.Rank.ID()))
		m.codePicker.SetValue(m.name.Code.ID(), vocabLabelFor(m.vocab, "nom_code", m.name.Code.ID()))
		if refID := core.PrimaryReferenceID(m.name.ReferenceID); refID != "" {
			// Sync GetReference — SQLite is quick and EnterEditModeCmd
			// isn't on a hot path. Falls back to the raw id if the ref
			// row is missing (dangling id from a prior deletion).
			label := refID
			if ref, err := m.a.GetReference(context.Background(), refID); err == nil {
				label = core.ReferenceLabel(ref)
			}
			m.referencePicker.SetValue(refID, label)
		}
		// Status comes from the raw col__status_id cached by GetName
		// (see core.NameRawStatus) — sflib's NomStatus enum can't
		// round-trip NOMEN URIs, so reading via m.name.Status.ID()
		// would show empty even for a name whose status is a URI.
		// Legacy CoLDP-generalized values (ESTABLISHED, ACCEPTABLE,
		// …) fall back to their derived display via vocabLabelFor.
		statusID := core.NameRawStatus(m.name.ID)
		statusLabel := core.NomenLabelFor(statusID)
		if statusLabel == "" {
			statusLabel = vocabLabelFor(m.vocab, "nom_status", statusID)
		}
		m.statusPicker.SetValue(statusID, statusLabel)
	}

	// Focus the first field and collect any startup cmds. focusField
	// returns a cmd (initial search for pickers, input focus for
	// textinputs); batch that with parent-name resolution.
	focusCmd := m.focusField(0)
	cmds := []tea.Cmd{focusCmd}
	if m.taxon.ParentID != "" {
		cmds = append(cmds, m.resolveParentName(m.taxon.ParentID))
	}
	return tea.Batch(cmds...), true
}

// vocabLabelFor looks up a term's display name for the given vocab.
// Returns the id itself if the vocab isn't loaded or the term isn't
// found — safer than an empty label since it at least shows something.
func vocabLabelFor(v *core.Vocabulary, name, id string) string {
	if id == "" {
		return ""
	}
	terms := vocabTerms(v, name)
	for _, t := range terms {
		if t.ID == id {
			if t.Name != "" {
				return t.Name
			}
			return t.ID
		}
	}
	return id
}

// ExitEditMode drops any unsaved changes and returns to view mode.
func (m *detailModel) ExitEditMode() {
	m.editing = false
	m.saveError = ""
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
}

// Creating reports whether the pane is composing a new taxon.
func (m detailModel) Creating() bool { return m.creating }

// CanCreate reports whether the pane can enter create mode — needs the
// archive to be editable. A root-level create is allowed even when no
// parent is selected (createParentID == "" means "add root taxon").
func (m detailModel) CanCreate() bool {
	return m.editable && !m.editing && !m.creating
}

// EnterCreateModeCmd puts the pane into create-mode with parentID as the
// prospective parent (empty → root). Loads the vocab bundle if needed
// (used by the rank picker) and focuses the sci-name input.
func (m *detailModel) EnterCreateModeCmd(parentID, parentLabel string) (tea.Cmd, bool) {
	if !m.CanCreate() {
		return nil, false
	}
	if m.vocab == nil && m.vocabErr == nil {
		m.vocab, m.vocabErr = m.a.Vocabulary(context.Background())
	}
	m.creating = true
	m.createStep = 0
	m.saveError = ""
	m.createParentID = parentID
	m.createParentName = parentLabel

	sci := textinput.New()
	sci.Prompt = ""
	sci.CharLimit = 500
	sci.Placeholder = "e.g. Panthera onca (Linnaeus, 1758)"
	m.createSciName = sci

	m.createCodePicker = newCombobox(vocabComboSource(m.vocab, "nom_code"), "nomenclatural code…")
	// Seed code from the parent's name so a new child under a
	// zoological genus defaults to ICZN, etc. Empty result means
	// no parent / no code set anywhere in ancestry — leave the picker
	// blank for the curator to choose.
	if parentCode, err := m.a.CodeForParent(context.Background(), parentID); err == nil && parentCode != "" {
		m.createCodePicker.SetValue(parentCode, vocabLabelFor(m.vocab, "nom_code", parentCode))
	}
	m.createFocus = 0
	return m.createSciName.Focus(), true
}

// ExitCreateMode discards the in-flight compose without touching the DB.
func (m *detailModel) ExitCreateMode() {
	m.creating = false
	m.createStep = 0
	m.saveError = ""
	m.createSciName.Blur()
	m.createRankPicker.Blur()
	m.createCodePicker.Blur()
	m.createStatusPicker.Blur()
	m.createRefPicker.Blur()
	for i := range m.createPreviewInputs {
		m.createPreviewInputs[i].Blur()
	}
}

// CreateSave is step-aware:
//   step 0 — validates verbatim + code, fires ParseNamePreview
//            (in-process via Archive), and — on success — advances
//            the form to step 1 with atomized fields pre-filled.
//   step 1 — commits via CreateName + CreateTaxon in one WithTx,
//            respecting curator overrides on any atomized field
//            (fill-gaps semantics inside CreateName ensures empty
//            fields still get parser defaults).
// On commit success emits savedMsg with parentMoved=true so the
// shell reveals the newly-created taxon in the tree.
func (m *detailModel) CreateSave() tea.Cmd {
	if m.createStep == 0 {
		return m.createAdvanceCmd()
	}
	return m.createCommitCmd()
}

// createAdvanceCmd runs ParseNamePreview and — via parsePreviewMsg —
// transitions the form to step 1. Runs in a goroutine so the tea
// event loop stays responsive even for pathological input.
func (m *detailModel) createAdvanceCmd() tea.Cmd {
	sci := strings.TrimSpace(m.createSciName.Value())
	if sci == "" {
		m.saveError = "scientific name is required"
		return nil
	}
	codeID := m.createCodePicker.SelectedID()
	if codeID == "" {
		m.saveError = "code is required — pick ICZN / ICN / ICNP / ICVCN / ICNCP"
		return nil
	}
	m.saveError = ""
	m.saving = true
	return m.parsePreviewCmd(sci, codeID)
}

// createCommitCmd writes the composed Name+Taxon pair. Called on
// Ctrl+S from step 1.
func (m *detailModel) createCommitCmd() tea.Cmd {
	sci := strings.TrimSpace(m.createSciName.Value())
	codeID := m.createCodePicker.SelectedID()
	parentID := m.createParentID
	name := m.composeCreateName(sci, codeID)
	a := m.a
	actor := m.actor
	m.saving = true
	return func() tea.Msg {
		ctx := contextWithActor(actor)
		var newTaxonID string
		err := a.WithTx(ctx, func(tx *core.Tx) error {
			nameID, err := tx.CreateName(name)
			if err != nil {
				return err
			}
			t := coldp.Taxon{
				NameID:   nameID,
				ParentID: parentID,
			}
			id, err := tx.CreateTaxon(t)
			if err != nil {
				return err
			}
			newTaxonID = id
			return nil
		})
		if err != nil {
			return savedMsg{err: err}
		}
		fresh, err := a.GetTaxon(ctx, newTaxonID)
		if err != nil {
			return savedMsg{err: err}
		}
		var freshName *coldp.Name
		if fresh.NameID != "" {
			freshName, _ = a.GetName(ctx, fresh.NameID)
		}
		var parentLabel string
		if fresh.ParentID != "" {
			if ref, refErr := a.TaxonRef(ctx, fresh.ParentID); refErr == nil {
				parentLabel = ref.Label.Text
			}
		}
		return savedMsg{
			taxon:       fresh,
			name:        freshName,
			parentLabel: parentLabel,
			// parentMoved reuses the reveal path — the newly-created
			// taxon needs the same "expand the tree down to me" treatment
			// as a reparented one.
			parentMoved: true,
		}
	}
}

// focusField blurs the previously focused field and focuses the new one.
// Returns a cmd for picker-triggered async work (e.g. initial vocab
// search for a rank picker showing all-options on empty input).
func (m *detailModel) focusField(idx int) tea.Cmd {
	// Blur whichever was previously focused. Picker fields have their
	// own Blur (which reverts pending input); textinputs use the
	// stdlib Blur.
	if p := m.pickerFor(m.focusedField); p != nil {
		p.Blur()
	} else if m.focusedField >= 0 && m.focusedField < len(m.inputs) {
		m.inputs[m.focusedField].Blur()
	}
	m.focusedField = idx
	if p := m.pickerFor(idx); p != nil {
		return p.Focus()
	}
	if idx >= 0 && idx < len(m.inputs) {
		return m.inputs[idx].Focus()
	}
	return nil
}

// fieldRange returns the effective form-field count — the full set when a
// name is attached, only the taxon block when it isn't.
func (m *detailModel) fieldRange() int {
	if m.nameEditable {
		return fieldCount
	}
	return firstNameField
}

func (m *detailModel) nextField() tea.Cmd {
	return m.focusField((m.focusedField + 1) % m.fieldRange())
}
func (m *detailModel) prevField() tea.Cmd {
	n := m.fieldRange()
	return m.focusField((m.focusedField - 1 + n) % n)
}

// Save produces a tea.Cmd that runs UpdateTaxon and (if name fields were
// touched) UpdateName in one WithTx, then reports the result via savedMsg.
// The col__modified snapshots taken at EnterEditMode drive If-Match on
// each aggregate — a concurrent edit on either side surfaces ErrConflict.
//
// Dirty detection is per-aggregate: if the taxon fields are all unchanged,
// UpdateTaxon is skipped so col__modified on the taxon doesn't bump. Same
// for the name.
func (m *detailModel) Save() tea.Cmd {
	if m.taxon == nil {
		return nil
	}
	a := m.a
	actor := m.actor
	taxonID := m.taxon.ID
	taxonIfMatch := m.taxonIfMatch

	// Snapshot form values so the goroutine doesn't race with the UI.
	tSnap := taxonFieldSnapshot{
		NamePhrase:      m.inputs[fieldNamePhrase].Value(),
		Scrutinizer:     m.inputs[fieldScrutinizer].Value(),
		ScrutinizerID:   m.inputs[fieldScrutinizerID].Value(),
		ScrutinizerDate: m.inputs[fieldScrutinizerDate].Value(),
		Link:            m.inputs[fieldLink].Value(),
		Remarks:         m.inputs[fieldTaxonRemarks].Value(),
	}
	switch m.extinctState {
	case 1:
		tSnap.Extinct = sql.NullBool{Bool: true, Valid: true}
	case 2:
		tSnap.Extinct = sql.NullBool{Bool: false, Valid: true}
	default:
		tSnap.Extinct = sql.NullBool{}
	}
	taxonDirty := tSnap != m.taxonOriginals

	var (
		nameID       string
		nameIfMatch  string
		nSnap        nameFieldSnapshot
		nameDirty    bool
		writeName    bool
	)
	if m.nameEditable && m.name != nil {
		nameID = m.name.ID
		nameIfMatch = m.nameIfMatch
		nSnap = nameFieldSnapshot{
			ScientificNameString: m.inputs[fieldScientificNameString].Value(),
			Authorship:           m.inputs[fieldAuthorship].Value(),
			// Rank / Code / Status / Reference are all combobox-driven
			// now — read from the picker's committed selection.
			RankID:                    m.rankPicker.SelectedID(),
			CodeID:                    m.codePicker.SelectedID(),
			StatusID:                  m.statusPicker.SelectedID(),
			ReferenceID:               m.referencePicker.SelectedID(),
			Uninomial:                 m.inputs[fieldUninomial].Value(),
			Genus:                     m.inputs[fieldGenus].Value(),
			InfragenericEpithet:       m.inputs[fieldInfrageneric].Value(),
			SpecificEpithet:           m.inputs[fieldSpecific].Value(),
			InfraspecificEpithet:      m.inputs[fieldInfraspecific].Value(),
			CultivarEpithet:           m.inputs[fieldCultivar].Value(),
			BasionymAuthorship:        m.inputs[fieldBasionymAuthor].Value(),
			BasionymAuthorshipYear:    m.inputs[fieldBasionymYear].Value(),
			CombinationAuthorship:     m.inputs[fieldCombAuthor].Value(),
			CombinationAuthorshipYear: m.inputs[fieldCombYear].Value(),
			PublishedInPage:           m.inputs[fieldPublishedInPage].Value(),
			Etymology:                 m.inputs[fieldEtymology].Value(),
			Remarks:                   m.inputs[fieldNameRemarks].Value(),
		}
		nameDirty = nSnap != m.nameOriginals
		writeName = nameDirty
	}

	// Parent move is a separate core operation from UpdateTaxon — dirty
	// check against the pre-edit snapshot decides whether to fire it.
	newParentID := m.parentPicker.SelectedID()
	moveParent := newParentID != m.parentOriginalID

	if !taxonDirty && !writeName && !moveParent {
		// Nothing to do — treat as a successful no-op save and exit edit mode.
		return func() tea.Msg {
			return savedMsg{taxon: m.taxon}
		}
	}

	m.saving = true
	return func() tea.Msg {
		ctx := contextWithActor(actor)
		err := a.WithTx(ctx, func(tx *core.Tx) error {
			// Order: MoveTaxon first (it bumps col__modified, which the
			// If-Match on UpdateTaxon compares against), then UpdateTaxon,
			// then UpdateName. Mirrors the PWA save flow so both frontends
			// agree on the fresh-token propagation.
			//
			// After MoveTaxon fires, the taxon's Modified changes; if the
			// caller had used the pre-move If-Match, UpdateTaxon would
			// reject with ErrConflict. We track a live taxon-match token
			// that starts as the pre-edit value and updates after a move.
			taxonMatch := taxonIfMatch
			if moveParent {
				if err := tx.MoveTaxon(taxonID, newParentID); err != nil {
					return err
				}
				fresh, err := a.GetTaxon(ctx, taxonID)
				if err != nil {
					return err
				}
				taxonMatch = fresh.Modified
			}
			if taxonDirty {
				// Read current so UpdateTaxon preserves fields the form
				// doesn't touch (UpdateTaxon overwrites every editable
				// column, so we hand back a merged struct).
				current, err := a.GetTaxon(ctx, taxonID)
				if err != nil {
					return err
				}
				current.NamePhrase = tSnap.NamePhrase
				current.Scrutinizer = tSnap.Scrutinizer
				current.ScrutinizerID = tSnap.ScrutinizerID
				current.ScrutinizerDate = tSnap.ScrutinizerDate
				current.Link = tSnap.Link
				current.Remarks = tSnap.Remarks
				current.Extinct = tSnap.Extinct
				current.Modified = taxonMatch // If-Match token (post-move if applicable)
				if err := tx.UpdateTaxon(*current); err != nil {
					return err
				}
			}
			if writeName {
				currentName, err := a.GetName(ctx, nameID)
				if err != nil {
					return err
				}
				currentName.ScientificNameString = nSnap.ScientificNameString
				currentName.Authorship = nSnap.Authorship
				currentName.Rank = core.ParseRank(nSnap.RankID)
				currentName.Code = nomcode.New(nSnap.CodeID)
				currentName.Uninomial = nSnap.Uninomial
				currentName.Genus = nSnap.Genus
				currentName.InfragenericEpithet = nSnap.InfragenericEpithet
				currentName.SpecificEpithet = nSnap.SpecificEpithet
				currentName.InfraspecificEpithet = nSnap.InfraspecificEpithet
				currentName.CultivarEpithet = nSnap.CultivarEpithet
				currentName.BasionymAuthorship = nSnap.BasionymAuthorship
				currentName.BasionymAuthorshipYear = nSnap.BasionymAuthorshipYear
				currentName.CombinationAuthorship = nSnap.CombinationAuthorship
				currentName.CombinationAuthorshipYear = nSnap.CombinationAuthorshipYear
				currentName.ReferenceID = nSnap.ReferenceID
				currentName.PublishedInPage = nSnap.PublishedInPage
				currentName.Etymology = nSnap.Etymology
				currentName.Remarks = nSnap.Remarks
				currentName.Modified = nameIfMatch
				if err := tx.UpdateName(*currentName); err != nil {
					return err
				}
				// Status goes through the raw-string path so NOMEN
				// URIs survive — see core.Tx.SetNameStatus.
				if err := tx.SetNameStatus(nameID, nSnap.StatusID); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return savedMsg{err: err}
		}
		fresh, err := a.GetTaxon(ctx, taxonID)
		if err != nil {
			return savedMsg{err: err}
		}
		// Refresh the name too so the display header / rank pick up any
		// changes on the next render.
		var freshName *coldp.Name
		if writeName {
			freshName, _ = a.GetName(ctx, nameID)
		}
		// Re-resolve the parent label — a MoveTaxon changes the target,
		// and the view pane needs the new human-readable display.
		var parentLabel string
		if fresh.ParentID != "" {
			if ref, refErr := a.TaxonRef(ctx, fresh.ParentID); refErr == nil {
				parentLabel = ref.Label.Text
			}
		}
		return savedMsg{
			taxon:       fresh,
			name:        freshName,
			parentLabel: parentLabel,
			parentMoved: moveParent,
		}
	}
}

func (m detailModel) Update(msg tea.Msg) (detailModel, tea.Cmd) {
	// Modal-first routing: while the add-reference modal is up, it owns
	// every message. This includes async completions (bhlLookupDone,
	// doiResolveDone, …) which are dispatched here by the tea runtime.
	// On modal exit, apply the pick (if any) and clear the modal.
	if m.addRef != nil {
		newModal, cmd := m.addRef.Update(msg)
		*m.addRef = newModal
		if m.addRef.Finished() {
			r := m.addRef.ConsumeResult()
			m.addRef = nil
			if r.id != "" {
				// SetValue writes both the picker's committed id and
				// its displayed label. Save's dirty check compares the
				// picker's SelectedID against nameOriginals.ReferenceID,
				// so a fresh pick surfaces as dirty and UpdateName fires
				// on the next Ctrl+S.
				m.referencePicker.SetValue(r.id, r.label)
			}
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case detailLoadedMsg:
		if m.current != "" && msg.taxonID != m.current {
			return m, nil // stale reply
		}
		m.err = msg.err
		m.taxon = msg.taxon
		m.name = msg.name
		m.parentLabel = msg.parentLabel
		m.loading = false
		return m, nil

	case savedMsg:
		m.saving = false
		if msg.err != nil {
			m.saveError = formatCoreError(msg.err)
			return m, nil
		}
		m.taxon = msg.taxon
		if msg.name != nil {
			m.name = msg.name
		}
		m.parentLabel = msg.parentLabel
		m.editing = false
		m.creating = false
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
		m.createSciName.Blur()
		m.createRankPicker.Blur()
		return m, nil

	case parentResolvedMsg:
		// Only apply if the parent picker still holds this id — the user
		// might have picked a different parent between request and reply.
		if m.parentPicker.SelectedID() == msg.id && msg.name != "" {
			m.parentPicker.SetValue(msg.id, msg.name)
		}
		return m, nil

	case parsePreviewMsg:
		// Result of the step-0 → step-1 transition. Populate the
		// preview inputs and focus the rank picker so the pre-selected
		// guess is where the curator lands.
		m.saving = false
		if msg.err != nil {
			m.saveError = formatCoreError(msg.err)
			return m, nil
		}
		m.initPreviewInputs(msg.preview)
		m.createStep = 1
		m.createFocus = cppRank
		return m, m.focusPreviewCurrent()

	case comboboxResultsMsg:
		// Route to the currently focused picker; race guard inside the
		// combobox drops stale-query messages.
		if p := m.pickerFor(m.focusedField); p != nil {
			newP, cmd := p.Update(msg)
			*p = newP
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		if m.creating {
			return m.updateCreate(msg)
		}
		if !m.editing {
			return m, nil
		}
		// Form-level keys always win over field-level input. Tab / Shift+Tab
		// commit the field's current picker/text state and move on; the
		// focused picker's Blur reverts uncommitted typing.
		switch msg.String() {
		case "tab":
			return m, m.nextField()
		case "shift+tab":
			return m, m.prevField()
		case " ":
			// Space still cycles the tri-state extinct field. Pickers get
			// it as normal typing (space is a valid character mid-search).
			if m.focusedField == fieldExtinct {
				m.extinctState = (m.extinctState + 1) % 3
				return m, nil
			}
		}
		// Picker fields own key handling for typing, ↓/↑ navigation,
		// Enter to commit, Esc to close the dropdown.
		if p := m.pickerFor(m.focusedField); p != nil {
			newP, cmd := p.Update(msg)
			*p = newP
			return m, cmd
		}
		// Text-input fields.
		if m.focusedField != fieldExtinct &&
			m.focusedField >= 0 && m.focusedField < len(m.inputs) {
			var cmd tea.Cmd
			m.inputs[m.focusedField], cmd = m.inputs[m.focusedField].Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// updateCreate routes keys in create mode, branching on step:
//   step 0 (verbatim): 2 fields — sci-name input + code picker.
//   step 1 (preview) : delegates to updateCreatePreview (many
//                      textinputs + 3 pickers, see createform.go).
// Esc from step 1 rolls back to step 0 preserving the verbatim/code
// so the curator can re-parse a corrected verbatim without retyping.
func (m detailModel) updateCreate(msg tea.KeyMsg) (detailModel, tea.Cmd) {
	if m.createStep == 1 {
		if msg.String() == "esc" {
			m.createStep = 0
			m.saveError = ""
			m.blurPreviewFocus()
			m.createFocus = 0
			return m, m.createSciName.Focus()
		}
		return m.updateCreatePreview(msg)
	}
	// Step 0 — old two-field flow.
	switch msg.String() {
	case "tab":
		return m.createFocusStep(1)
	case "shift+tab":
		return m.createFocusStep(-1)
	}
	if m.createFocus == 1 {
		var cmd tea.Cmd
		m.createCodePicker, cmd = m.createCodePicker.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.createSciName, cmd = m.createSciName.Update(msg)
	return m, cmd
}

// createFocusStep cycles focus between step-0's two fields (sci-name
// input + code picker). Step 1 has its own focus cycle handled by
// createPreviewFocusStep in createform.go.
func (m detailModel) createFocusStep(delta int) (detailModel, tea.Cmd) {
	if m.createFocus == 0 {
		m.createSciName.Blur()
	} else {
		m.createCodePicker.Blur()
	}
	m.createFocus = ((m.createFocus + delta) + 2) % 2
	if m.createFocus == 0 {
		return m, m.createSciName.Focus()
	}
	return m, m.createCodePicker.Focus()
}

// SetCurrent updates the ID the pane is displaying. Also drops any active
// edit — switching taxa mid-edit throws away unsaved changes; a real UX
// would prompt for confirmation but that's beyond the walking skeleton.
func (m *detailModel) SetCurrent(id string) {
	m.current = id
	m.loading = true
	m.taxon = nil
	m.name = nil
	m.parentLabel = ""
	m.err = nil
	m.ExitEditMode()
}

// View renders the detail pane. In edit mode, the form takes over below
// the header; view mode shows the labelled field list. In create mode a
// compact new-taxon form replaces the pane entirely.
func (m detailModel) View() string {
	// Modal takes over the pane completely while it's open — no header,
	// no field list, just the modal. The parent field values are safe;
	// the picker gets set only on a confirmed pick and any half-done
	// modal state evaporates when the modal closes.
	if m.addRef != nil {
		return m.addRef.View()
	}
	if m.creating {
		return m.renderCreate()
	}
	if m.current == "" {
		return dimStyle.Render("(no taxon selected)")
	}
	if m.err != nil {
		return errStyle.Render("error: " + m.err.Error())
	}
	if m.loading || m.taxon == nil {
		return dimStyle.Render("loading…")
	}

	var b strings.Builder
	header := m.headerLine()
	// headerLine already applies bold + italic segment-by-segment so it
	// isn't wrapped in headerStyle here. Width computation uses the plain
	// taxon name (no escapes) so the underline matches visible glyphs.
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", max(20, m.headerWidth()))))
	b.WriteByte('\n')

	if m.editing {
		b.WriteString(m.renderForm())
	} else {
		b.WriteString(m.renderFields())
	}
	return b.String()
}

// renderCreate draws the three-field new-taxon form (sci-name, rank,
// code). The parent context header makes it obvious what the new taxon
// will be a child of. Code is seeded from the parent's code by default
// so ICZN work stays ICZN with no clicks; curator can override.
func (m detailModel) renderCreate() string {
	if m.createStep == 1 {
		return m.renderCreatePreview()
	}
	var b strings.Builder
	parent := m.createParentName
	if parent == "" {
		parent = "(root)"
	}
	header := headerStyle.Render("New taxon under ") + parent +
		"  " + dimStyle.Render("(step 1 / 2 — verbatim)")
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", 40)))
	b.WriteString("\n\n")

	// Sci-name row. Required — the "*" is rendered in the same faint
	// style but with a red-ish tint via errStyle so it draws the eye
	// without shouting. Rank has moved to step 2 (comes from RankGuess).
	sciLabel := labelStyle.Render(fmt.Sprintf("%-14s", "Scientific name")) +
		errStyle.Render("* ") + labelStyle.Render(": ")
	b.WriteString(sciLabel)
	b.WriteString(m.createSciName.View())
	b.WriteByte('\n')

	// Code row. Required.
	codeLabel := labelStyle.Render(fmt.Sprintf("%-14s", "Code")) +
		errStyle.Render("* ") + labelStyle.Render(": ")
	b.WriteString(codeLabel)
	b.WriteString(m.createCodePicker.View())
	b.WriteString("\n\n")

	if m.saveError != "" {
		b.WriteString(errStyle.Render(m.saveError))
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	if m.saving {
		b.WriteString(dimStyle.Render("parsing…"))
	} else {
		b.WriteString(dimStyle.Render("[tab] next field   [ctrl+s] next → preview   [esc] cancel"))
	}
	return b.String()
}

func (m detailModel) renderFields() string {
	var b strings.Builder

	// Row (label, value) helper. Skips empty values to keep the pane
	// scannable — a curator shouldn't have to page past "Etymology: (empty)"
	// on 90% of taxa. Modified / ModifiedBy always render since they're
	// meaningful even when populated automatically.
	push := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("%-14s", k+":")))
		b.WriteByte(' ')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	// Force-render even when empty (useful for boolean audit fields).
	pushAlways := func(k, v string) {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%-14s", k+":")))
		b.WriteByte(' ')
		b.WriteString(v)
		b.WriteByte('\n')
	}

	// Taxon identity. Parent renders as its resolved label (falls back
	// to the raw id if the lookup failed or the parent row is missing).
	push("ID", m.taxon.ID)
	parent := m.parentLabel
	if parent == "" {
		parent = m.taxon.ParentID
	}
	push("Parent", parent)
	push("Rank", m.rankString())
	pushAlways("Status", m.statusString())
	pushAlways("Extinct", nullableBool(m.taxon.Extinct))

	// Name-derived fields — the "why is this thing this?" info. Only
	// rendered when a name is attached; legacy taxa without one skip
	// this block entirely. Nom status resolves via NOMEN when the
	// stored value is a URI; legacy CoLDP-generalized values fall
	// through as their raw ID.
	if m.name != nil {
		push("Authorship", m.name.Authorship)
		push("Nom code", m.name.Code.ID())
		statusRaw := core.NameRawStatus(m.name.ID)
		if lbl := core.NomenLabelFor(statusRaw); lbl != "" {
			push("Nom status", lbl)
		} else {
			push("Nom status", statusRaw)
		}
		// Atomized authorship rows — only render when populated so a
		// record with just verbatim authorship stays scannable.
		// Botanical records typically show both; zoological records
		// often just show basionym.
		if a, y := m.name.BasionymAuthorship, m.name.BasionymAuthorshipYear; a != "" || y != "" {
			push("Basionym", joinNonEmpty(a, y))
		}
		if a, y := m.name.CombinationAuthorship, m.name.CombinationAuthorshipYear; a != "" || y != "" {
			push("Combination", joinNonEmpty(a, y))
		}
		push("Published year", m.name.PublishedInYear)
		// Reference: resolved to a display label via GetReference when a
		// primary id is present. Falls back to the raw id on lookup
		// failure so a curator can still see something to fix.
		if refID := core.PrimaryReferenceID(m.name.ReferenceID); refID != "" {
			label := refID
			if ref, err := m.a.GetReference(context.Background(), refID); err == nil {
				label = core.ReferenceLabel(ref)
			}
			push("Reference", label)
		}
		push("Etymology", m.name.Etymology)
		push("Name link", m.name.Link)
		push("Name remarks", m.name.Remarks)
	}

	// Taxon extras (only when set).
	push("Name phrase", m.taxon.NamePhrase)
	push("Scrutinizer", m.taxon.Scrutinizer)
	push("Link", m.taxon.Link)
	push("Remarks", m.taxon.Remarks)

	// Audit trail — always shown so curators can see when/who last touched
	// this row without a separate "history" click.
	pushAlways("Modified", m.taxon.Modified)
	push("Modified by", m.taxon.ModifiedBy)
	if m.name != nil && m.name.Modified != m.taxon.Modified {
		pushAlways("Name modified", m.name.Modified)
		push("Name mod. by", m.name.ModifiedBy)
	}

	if m.editable {
		b.WriteByte('\n')
		b.WriteString(dimStyle.Render("[e] edit"))
	}
	return b.String()
}

func (m detailModel) renderForm() string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("── Taxon ──"))
	b.WriteByte('\n')
	limit := m.fieldRange()
	for i := range limit {
		// Section headers before boundary fields. The extended edit
		// form now shows ~26 fields (verbatim + atomized name + two
		// authorship pairs + reference + notes + taxon extras) — the
		// headers keep it scannable without a progressive-disclosure
		// toggle (terminals have vertical space; hiding fields would
		// complicate tab navigation).
		switch i {
		case firstNameField:
			b.WriteByte('\n')
			b.WriteString(sectionHeaderStyle.Render("── Name ──"))
			b.WriteByte('\n')
		case fieldUninomial:
			b.WriteByte('\n')
			b.WriteString(sectionHeaderStyle.Render("── Atomized name ──"))
			b.WriteByte('\n')
		case fieldBasionymAuthor:
			b.WriteByte('\n')
			b.WriteString(sectionHeaderStyle.Render("── Basionym (original) ──"))
			b.WriteByte('\n')
		case fieldCombAuthor:
			b.WriteByte('\n')
			b.WriteString(sectionHeaderStyle.Render("── Combination (current) ──"))
			b.WriteByte('\n')
		case fieldNomStatus:
			b.WriteByte('\n')
			b.WriteString(sectionHeaderStyle.Render("── Metadata ──"))
			b.WriteByte('\n')
		}
		b.WriteString(m.renderFormRow(i))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if m.saveError != "" {
		b.WriteString(errStyle.Render(m.saveError))
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	if m.saving {
		b.WriteString(dimStyle.Render("saving…"))
	} else {
		b.WriteString(dimStyle.Render("[tab] next   [shift+tab] prev   [space] cycle extinct   [ctrl+a] add reference   [ctrl+s] save   [esc] cancel"))
	}
	return b.String()
}

// renderFormRow draws a single label + input line. Picker-driven fields
// (extinct tri-state and the four combobox pickers) render via their own
// helpers; everything else renders the textinput. Combobox output can
// span multiple lines (the dropdown) — the caller's newline between
// rows still works because the dropdown lines are part of the picker's
// own View() output.
func (m detailModel) renderFormRow(i int) string {
	label := labelStyle.Render(fmt.Sprintf("%-16s", fieldLabels[i]+":"))
	var rendered string
	switch {
	case i == fieldExtinct:
		opts := []string{"(unset)", "yes", "no"}
		for j, opt := range opts {
			if j == m.extinctState {
				opts[j] = focusedStyle.Render("[" + opt + "]")
			} else {
				opts[j] = dimStyle.Render(" " + opt + " ")
			}
		}
		rendered = strings.Join(opts, " ")
		if m.focusedField == fieldExtinct {
			rendered += "  " + dimStyle.Render("(space to cycle)")
		}
	case fieldIsPicker(i):
		// Copy the picker by value for View — we're already inside a
		// value receiver on detailModel, so this preserves the invariant
		// that render never mutates.
		rendered = pickerViewFor(&m, i)
	default:
		rendered = m.inputs[i].View()
	}
	return label + " " + rendered
}

// pickerViewFor picks the right combobox instance for a field and calls
// its View. Kept as a free function so the switch mirrors pickerFor's.
func pickerViewFor(m *detailModel, f int) string {
	if p := m.pickerFor(f); p != nil {
		return p.View()
	}
	return ""
}

// headerLine renders the taxon heading with the same italicization rules
// as the PWA's HTML label: canonical wrapped in italic when
// core.ItalicForRank says so, dagger prefix for extinct taxa, authorship
// stays roman. Each segment is rendered separately with lipgloss so the
// bold + italic combination works — a single outer lipgloss.Render would
// emit a mid-line reset that clears bold before authorship. The two
// frontends stay visually aligned via the shared core.ItalicForRank rule.
func (m detailModel) headerLine() string {
	extinct := m.taxon != nil && m.taxon.Extinct.Valid && m.taxon.Extinct.Bool
	dagger := ""
	if extinct {
		dagger = "† "
	}
	if m.name == nil {
		return headerStyle.Render(dagger + "(no name)")
	}
	canonical := m.name.CanonicalSimple
	if canonical == "" {
		canonical = m.name.ScientificName
	}
	canonStyle := headerStyle
	if core.ItalicForRank(m.name.Rank.ID()) && canonical != "" {
		canonStyle = canonStyle.Italic(true)
	}
	parts := []string{headerStyle.Render(dagger) + canonStyle.Render(canonical)}
	if m.name.Authorship != "" {
		parts = append(parts, headerStyle.Render(" "+m.name.Authorship))
	}
	return strings.Join(parts, "")
}

// headerWidth returns the visible rune-width of the plain header text
// (dagger + canonical + authorship), used to size the underline. The
// styled header carries SGR escapes that inflate strings.Len; we count
// glyphs instead.
func (m detailModel) headerWidth() int {
	extinct := m.taxon != nil && m.taxon.Extinct.Valid && m.taxon.Extinct.Bool
	dagger := ""
	if extinct {
		dagger = "† "
	}
	if m.name == nil {
		return len([]rune(dagger + "(no name)"))
	}
	canonical := m.name.CanonicalSimple
	if canonical == "" {
		canonical = m.name.ScientificName
	}
	plain := dagger + canonical
	if m.name.Authorship != "" {
		plain += " " + m.name.Authorship
	}
	return len([]rune(plain))
}

func (m detailModel) rankString() string {
	if m.name == nil {
		return ""
	}
	return m.name.Rank.ID()
}

func (m detailModel) statusString() string {
	if m.taxon.Provisional.Valid && m.taxon.Provisional.Bool {
		return "provisionally accepted"
	}
	return "accepted"
}

// vocabTerms returns the terms for a named vocabulary from the loaded
// bundle, or an empty slice if the bundle isn't loaded or the name is
// unknown. Kept tolerant so a load failure degrades gracefully to
// "no options" rather than a panic on a background render.
func vocabTerms(v *core.Vocabulary, name string) []core.VocabTerm {
	if v == nil {
		return nil
	}
	switch name {
	case "nom_code":
		return v.NomCode
	case "nom_status":
		return v.NomStatus
	case "taxonomic_status":
		return v.TaxonomicStatus
	case "rank":
		return v.Rank
	case "gender":
		return v.Gender
	}
	return nil
}

// joinNonEmpty produces "a, b" from two strings, dropping either when
// empty. Used to render "Basionym: Linnaeus, 1758" (or just "Linnaeus"
// / just "1758") on the view pane without empty commas.
func joinNonEmpty(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + ", " + b
	case a != "":
		return a
	}
	return b
}

func nullableBool(b sql.NullBool) string {
	if !b.Valid {
		return ""
	}
	if b.Bool {
		return "yes"
	}
	return "no"
}

// formatCoreError produces a friendly one-line rendering of core sentinel
// errors. Falls back to err.Error() for anything else.
func formatCoreError(err error) string {
	switch {
	case errors.Is(err, core.ErrConflict):
		return "conflict: someone else edited this taxon — cancel and re-open to see the latest, then reapply your change."
	case errors.Is(err, core.ErrValidation):
		return "validation failed: " + err.Error()
	case errors.Is(err, core.ErrNotFound):
		return "not found — the taxon may have been deleted."
	case errors.Is(err, core.ErrReadOnly):
		return "archive is read-only."
	}
	return err.Error()
}

var (
	headerStyle        = lipgloss.NewStyle().Bold(true)
	labelStyle         = lipgloss.NewStyle().Faint(true)
	focusedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	sectionHeaderStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	// Star rating styles for Confidence in the metadata view.
	// Yellow-ish for filled, dim for the empty pips.
	starFilledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	starEmptyStyle  = lipgloss.NewStyle().Faint(true)
)
