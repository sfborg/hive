package refmanager

import (
	"regexp"
	"sort"
	"strings"

	"github.com/sfborg/gsvalidator/domain"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
)

// citationCoverageValidator flags references whose attached-PDF
// JATS sidecar cites figure/table labels that weren't extracted
// from the PDF — a strong signal that GROBID under-detected the
// paper's figures or tables. If the body text says "Figure 3" but
// no <fig> with a "Figure 3" caption exists, one of two things is
// true: GROBID missed a figure, or the caption was mis-labeled.
// Either way it's worth surfacing so a curator can draw a bbox for
// the missed region using the correction tool.
//
// Also feeds the ML training arc — cited-but-missing labels are
// automatically-detectable examples of extractor under-recall, no
// curator judgment required to identify.
//
// One row per reference (not per missing label). Message lists all
// missing labels sorted for stable output. Silent skip when the
// reference has no attached PDFs or when the sidecar isn't on disk.
type citationCoverageValidator struct {
	archivePath string
}

func newCitationCoverageValidator(archivePath string) *citationCoverageValidator {
	return &citationCoverageValidator{archivePath: archivePath}
}

func (v *citationCoverageValidator) Name() string {
	return "hive.citation_coverage"
}

func (v *citationCoverageValidator) CanAutoFix() bool { return false }

func (v *citationCoverageValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *citationCoverageValidator) Validate(
	ctx *domain.ValidationContext, rule *domain.Rule,
) (*domain.Result, error) {
	pass := &domain.Result{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		RecordID:    ctx.RecordID,
		TableName:   ctx.TableName,
		FieldName:   rule.FieldName,
		Passed:      true,
		Enforcement: domain.EnforcementSoft,
		Severity:    domain.SeverityWarn,
	}

	rows, err := ctx.DB.QueryContext(ctx.Ctx,
		`SELECT sha512 FROM hive__reference_pdf WHERE reference_id = ?`,
		ctx.RecordID,
	)
	if err != nil {
		return pass, nil
	}
	defer rows.Close()

	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			continue
		}
		shas = append(shas, sha)
	}
	if len(shas) == 0 {
		return pass, nil
	}

	for _, sha := range shas {
		data, err := sidecar.Read(v.archivePath, sha, "jats.xml")
		if err != nil || data == nil {
			continue
		}
		missing := findMissingFigTableLabels(data)
		if len(missing) == 0 {
			continue
		}
		msg := rule.WarningMessage
		if msg == "" {
			msg = "Body text cites figure/table labels that were not extracted from the PDF."
		}
		short := sha
		if len(short) > 12 {
			short = short[:12]
		}
		return &domain.Result{
			RuleID:      rule.ID,
			RuleName:    rule.Name,
			RecordID:    ctx.RecordID,
			TableName:   ctx.TableName,
			FieldName:   rule.FieldName,
			Passed:      false,
			Enforcement: domain.EnforcementSoft,
			Severity:    domain.SeverityWarn,
			Message:     msg + " " + short + ": missing " + strings.Join(missing, ", "),
			ActualValue: sha,
		}, nil
	}
	return pass, nil
}

// figBlockRegex matches an entire <fig>...</fig> or
// <table-wrap>...</table-wrap> block, DOTALL so the block can span
// newlines. Used to strip extracted-label content out of the body
// scan so a fig's own caption isn't mistaken for a citation.
var figBlockRegex = regexp.MustCompile(`(?s)<(fig|table-wrap)\b[^>]*>.*?</(fig|table-wrap)>`)

// citationLabelRegex matches "Figure N", "Fig N", "Fig. N",
// "Table N", "Tab. N" (case-insensitive) — the label forms authors
// use in body prose. Same shape as jatsconv's figLabelRegex; kept
// local so this file doesn't reach across the pkg boundary.
var citationLabelRegex = regexp.MustCompile(`(?i)\b(fig(?:ure)?|tab(?:le)?)\.?\s*(\d+)\b`)

// findMissingFigTableLabels scans a JATS sidecar for figure/table
// labels cited in body prose that don't have a matching extracted
// <fig> or <table-wrap>. Returns the sorted list of missing labels
// ("Figure 3", "Table 2", ...); empty when everything cited exists.
//
// Approach:
//  1. Collect the "present" set by scanning each <fig>/<table-wrap>
//     block for its "Figure N" / "Table N" caption prefix.
//  2. Strip those blocks from the body, then scan the remainder for
//     "Figure N" / "Table N" mentions. That set is the "cited" set.
//  3. Missing = cited − present.
//
// Stripping is deliberate — a fig's own caption starts with its
// label ("Figure 3."), which would otherwise get counted as a
// citation of itself and mask the miss.
func findMissingFigTableLabels(jats []byte) []string {
	present := make(map[string]bool)
	for _, block := range figBlockRegex.FindAll(jats, -1) {
		m := citationLabelRegex.FindSubmatch(block)
		if label := labelFromMatch(m); label != "" {
			present[label] = true
		}
	}
	// Comments hold removed text (hive:noise wrappers), which is not
	// part of the paper's prose.
	body := commentRegex.ReplaceAll(figBlockRegex.ReplaceAll(jats, nil), nil)
	cited := make(map[string]bool)
	for _, m := range citationLabelRegex.FindAllSubmatch(body, -1) {
		if label := labelFromMatch(m); label != "" {
			cited[label] = true
		}
	}
	var missing []string
	for label := range cited {
		if !present[label] {
			missing = append(missing, label)
		}
	}
	sort.Strings(missing)
	return missing
}

// labelFromMatch normalizes a citationLabelRegex submatch into a
// canonical "Figure N" or "Table N" key. Empty when the match's
// first group isn't a figure or table keyword.
func labelFromMatch(m [][]byte) string {
	if m == nil {
		return ""
	}
	kind := strings.ToLower(string(m[1]))
	num := string(m[2])
	switch {
	case strings.HasPrefix(kind, "fig"):
		return "Figure " + num
	case strings.HasPrefix(kind, "tab"):
		return "Table " + num
	}
	return ""
}
