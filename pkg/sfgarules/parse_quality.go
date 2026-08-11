package sfgarules

import (
	"fmt"

	"github.com/gdower/gsvalidator/domain"
)

// ParseQualityValidator surfaces gnparser's quality score for a name.
// gnparser assigns gn__parse_quality on every parse; the numeric scale
// is defined in gnparser/ent/parsed/warning.go's WarningQualityMap and
// derived per name as the max quality of any warning that fired (with
// 1 == clean parse, 0 == parsing failed outright):
//
//	0 → parsing failed (typo, stray characters, non-name text)
//	1 → no warnings, clean parse
//	2 → trivial parser warnings (unknown author, ambiguous filius,
//	    unusual capitalization). Name parses fine but the parser
//	    noticed something worth confirming.
//	3 → moderate warnings (unusual rank, HTML entities, dot-embedded
//	    epithet, year range, …).
//	4 → severe warnings — TailWarn (unparsed remainder — sign of
//	    concatenated junk), GenusAbbrWarn, LowCaseWarn, missing
//	    authorship parens, UTF-8 conversion issues, etc.
//
// Severity: quality 0 escalates to error regardless of the rule's own
// severity (an unparseable name is almost always a fixable typo).
// Every other non-clean tier fires at the rule's severity (warn by
// default) — the curator's action is to open the record and confirm
// the atomized fields (canonical, epithets, authorship parts) capture
// what they intended. Fixing the raw string to silence the parser is
// rarely the right move.
type ParseQualityValidator struct{}

// Name returns the validator type identifier.
func (v *ParseQualityValidator) Name() string {
	return "sfga_parse_quality"
}

// Validate reads gn__parse_quality off the record and emits a result
// keyed to the parser's score.
func (v *ParseQualityValidator) Validate(ctx *domain.ValidationContext, rule *domain.Rule) (*domain.Result, error) {
	result := domain.NewResult(ctx, rule)
	result.FieldName = "gn__parse_quality"

	raw, exists := ctx.GetFieldValue("gn__parse_quality")
	if !exists || raw == nil {
		result.Passed = true
		result.Message = "No parse quality recorded"
		return result, nil
	}
	quality, ok := toInt(raw)
	if !ok {
		result.Passed = true
		result.Message = "Parse quality not numeric"
		return result, nil
	}

	if quality == 1 {
		result.Passed = true
		result.Message = "Parsed cleanly"
		result.ActualValue = quality
		return result, nil
	}

	result.Passed = false
	result.ActualValue = quality
	result.ExpectedValue = 1

	// Quality 0 escalates to error regardless of the rule's severity
	// (unparseable → almost always a fixable typo). Every other tier
	// keeps whatever severity the rule specifies; messages direct the
	// curator to check the atomized fields rather than the raw string.
	switch quality {
	case 0:
		result.Severity = domain.SeverityError
		result.ValidationType = string(domain.SeverityError)
		result.Message = "Scientific name could not be parsed. Check for typos, stray characters, or missing spaces."
	case 2:
		result.Message = "Parser recorded minor warnings on this name (quality 2). Open the record and confirm the atomized fields (canonical, epithets, authorship) match what you intended."
	case 3:
		result.Message = "Parser recorded notable warnings on this name (quality 3). Open the record and confirm the atomized fields (canonical, epithets, authorship) match what you intended."
	default:
		result.Message = fmt.Sprintf("Parser recorded severe warnings on this name (quality %d). Open the record and check both the atomized fields and the raw string.", quality)
	}
	if rule.WarningMessage != "" && quality != 0 {
		result.Message = rule.WarningMessage
	}
	return result, nil
}

// toInt best-effort converts a value read from a SQL row into an int.
// modernc.org/sqlite typically hands back int64 for INTEGER columns,
// but different driver paths may surface float64 or string.
func toInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case float32:
		return int(t), true
	case string:
		var n int
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

// CanAutoFix returns false (parse quality reflects the value already
// stored; only the curator can fix the underlying string).
func (v *ParseQualityValidator) CanAutoFix() bool {
	return false
}

// AutoFix is not supported for parse-quality.
func (v *ParseQualityValidator) AutoFix(ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result) error {
	return domain.ErrAutoFixFailed
}
