package hive

import (
	"strings"

	"github.com/gdower/gsvalidator/domain"
	"github.com/sfborg/hive/pkg/jatsconv"
	"github.com/sfborg/hive/pkg/sidecar"
)

// jatsValidationValidator runs xmllint against each attached
// PDF's augmented-JATS sidecar and flags any reference whose
// sidecar fails schema validation against the hive-jats.rng
// overlay (JATS Publishing 1.3 + tp:/gn: extensions).
//
// Purpose: developer telemetry surfaced through the same Issues
// dashboard as data-quality issues. A JATS-validation failure
// means jatsconv or annotator produced structurally invalid
// output — a curator can't fix that by editing anything, but the
// row in the dashboard tells us (developers) that a specific
// reference's pipeline output is broken and worth investigating.
//
// Silent-skip paths:
//   - xmllint not installed on this host (Validate returns
//     Executed=false).
//   - Reference has no attached PDFs (nothing to validate).
//   - Attached sha has no .jats.xml sidecar (upload never
//     generated one; a separate issue class).
//
// One row per failing reference, not per failing sha — the
// dashboard's identity (table, record, rule, field) is unique
// per reference and later shas' failures overwrite earlier ones
// on reindex. In practice a reference usually has one PDF; the
// multi-PDF case gets the first-failing sha's error.
type jatsValidationValidator struct {
	archivePath string
}

func newJATSValidationValidator(archivePath string) *jatsValidationValidator {
	return &jatsValidationValidator{archivePath: archivePath}
}

func (v *jatsValidationValidator) Name() string {
	return "hive.jats_validation"
}

func (v *jatsValidationValidator) CanAutoFix() bool { return false }

func (v *jatsValidationValidator) AutoFix(
	ctx *domain.ValidationContext, rule *domain.Rule, result *domain.Result,
) error {
	return nil
}

func (v *jatsValidationValidator) Validate(
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
		// Query error (missing table on a legacy archive, DB busy,
		// etc.) — treat as pass so we don't spam issues over an
		// infrastructure hiccup unrelated to the reference's data.
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
		res, err := jatsconv.Validate(data)
		if err != nil {
			continue
		}
		if !res.Executed {
			// xmllint unavailable — silent skip. Docker ships it so
			// this only affects local Go-native runs.
			return pass, nil
		}
		if res.Valid {
			continue
		}
		msg := rule.WarningMessage
		if msg == "" {
			msg = "JATS validation failed for sidecar"
		}
		errSummary := strings.Join(res.Errors, "; ")
		// Cap at 200 chars so the row's message column stays
		// readable — the full error list is regenerable via
		// reindex + xmllint if a developer needs it.
		if len(errSummary) > 200 {
			errSummary = errSummary[:197] + "..."
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
			Message:     msg + " " + short + ": " + errSummary,
			ActualValue: sha,
		}, nil
	}
	return pass, nil
}
