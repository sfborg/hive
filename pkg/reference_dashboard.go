package hive

import (
	"context"
	"fmt"
)

// ReferenceDashboard is the aggregated progress view shown as the
// landing pane of the references section. Each of the four steps
// counts how many references have completed it; the attention list
// surfaces the specific refs that are next in the queue.
//
// See REFERENCES_DASHBOARD_PLAN.md for the workflow this reflects
// (bibliographic → digitize → extract facts → import).
type ReferenceDashboard struct {
	Total     int
	Steps     ReferenceDashboardSteps
	Attention []ReferenceDashboardAttention
}

// ReferenceDashboardSteps holds per-step done/total counts. Step
// N's "done" is the number of references that have completed at
// least through step N (not the number strictly parked there).
type ReferenceDashboardSteps struct {
	Bibliographic       ReferenceDashboardStep
	Digitized           ReferenceDashboardStep
	AssertionsExtracted ReferenceDashboardStep
	Imported            ReferenceDashboardStep
}

type ReferenceDashboardStep struct {
	Done  int
	Total int
}

// ReferenceDashboardAttention flags one bucket of references
// waiting on the given step. Step is the step number the ref is
// blocked on (i.e., the ref has completed step Step-1 but not Step
// itself). Count is how many such refs exist. OldestDays is
// deferred to a future revision — always 0 in v1 (see the plan
// doc's Deliberately deferred section).
type ReferenceDashboardAttention struct {
	Step        int
	Description string
	Count       int
	OldestDays  int
}

// ReferenceDashboard computes the landing-pane view in one SQL
// round-trip. Step-completion definitions:
//
//   1. Bibliographic — col__author, col__issued, col__title all
//      non-empty. Enough to compose a citation and match against
//      external DBs. (col__issued is the sfga column for
//      publication date; yearFromIssued derives the year when
//      needed for display.)
//   2. Digitized — has at least one attached PDF (row in
//      hive__reference_pdf). v1 treats "PDF attached" as "JATS
//      sidecar produced" since successful attach triggers the
//      GROBID + jatsconv pipeline. Sidecar-invalid cases still
//      surface through the hive_jats_validation issue rule.
//   3. Assertions extracted — has at least one
//      hive__reference_assertion row. Both origin='model' and
//      origin='curator' count — the step is about "structured
//      claims exist for review," not about which side produced
//      them.
//   4. Imported — has at least one hive__reference_assertion row
//      with imported_at set. Any imported assertion means the
//      reference has contributed data to the archive.
//
// Attention rows surface the three "waiting on step N" buckets in
// v1: needs-digitize (has ref, no PDF), needs-extract (has PDF, no
// assertions), needs-review (has assertions, none imported).
// Sorted by step number so the hot list reads top-down in
// workflow order.
func (a *Archive) ReferenceDashboard(ctx context.Context) (*ReferenceDashboard, error) {
	const q = `
		SELECT
			COUNT(*) AS total,
			SUM(CASE
				WHEN COALESCE(r.col__author, '') != ''
				 AND COALESCE(r.col__issued, '') != ''
				 AND COALESCE(r.col__title, '')  != ''
				THEN 1 ELSE 0
			END) AS bib_done,
			SUM(CASE
				WHEN EXISTS(SELECT 1 FROM hive__reference_pdf p
				            WHERE p.reference_id = r.col__id)
				THEN 1 ELSE 0
			END) AS digitized_done,
			SUM(CASE
				WHEN EXISTS(SELECT 1 FROM hive__reference_assertion a
				            WHERE a.reference_id = r.col__id)
				THEN 1 ELSE 0
			END) AS assertions_done,
			SUM(CASE
				WHEN EXISTS(SELECT 1 FROM hive__reference_assertion a
				            WHERE a.reference_id = r.col__id
				              AND a.imported_at IS NOT NULL)
				THEN 1 ELSE 0
			END) AS imported_done,
			SUM(CASE
				WHEN NOT EXISTS(SELECT 1 FROM hive__reference_pdf p
				                WHERE p.reference_id = r.col__id)
				THEN 1 ELSE 0
			END) AS needs_digitize,
			SUM(CASE
				WHEN EXISTS(SELECT 1 FROM hive__reference_pdf p
				            WHERE p.reference_id = r.col__id)
				 AND NOT EXISTS(SELECT 1 FROM hive__reference_assertion a
				                WHERE a.reference_id = r.col__id)
				THEN 1 ELSE 0
			END) AS needs_extract,
			SUM(CASE
				WHEN EXISTS(SELECT 1 FROM hive__reference_assertion a
				            WHERE a.reference_id = r.col__id)
				 AND NOT EXISTS(SELECT 1 FROM hive__reference_assertion a
				                WHERE a.reference_id = r.col__id
				                  AND a.imported_at IS NOT NULL)
				THEN 1 ELSE 0
			END) AS needs_review
		FROM reference r
	`
	var (
		total, bib, digitized, assertions, imported                  int
		needsDigitize, needsExtract, needsReview                     int
		bibNull, digNull, assertionsNull, importedNull               any
		needsDigNull, needsExtNull, needsRevNull                     any
	)
	// Scan into any for the SUM columns because SQLite
	// returns NULL for SUM over zero rows. Convert below with a
	// helper that treats NULL as 0.
	if err := a.db.QueryRowContext(ctx, q).Scan(
		&total, &bibNull, &digNull, &assertionsNull, &importedNull,
		&needsDigNull, &needsExtNull, &needsRevNull,
	); err != nil {
		return nil, fmt.Errorf("core: reference dashboard: %w", err)
	}
	bib = intOrZero(bibNull)
	digitized = intOrZero(digNull)
	assertions = intOrZero(assertionsNull)
	imported = intOrZero(importedNull)
	needsDigitize = intOrZero(needsDigNull)
	needsExtract = intOrZero(needsExtNull)
	needsReview = intOrZero(needsRevNull)

	d := &ReferenceDashboard{
		Total: total,
		Steps: ReferenceDashboardSteps{
			Bibliographic:       ReferenceDashboardStep{Done: bib, Total: total},
			Digitized:           ReferenceDashboardStep{Done: digitized, Total: total},
			AssertionsExtracted: ReferenceDashboardStep{Done: assertions, Total: total},
			Imported:            ReferenceDashboardStep{Done: imported, Total: total},
		},
	}
	if needsDigitize > 0 {
		d.Attention = append(d.Attention, ReferenceDashboardAttention{
			Step:        2,
			Description: "waiting on digitize (no PDF attached)",
			Count:       needsDigitize,
		})
	}
	if needsExtract > 0 {
		d.Attention = append(d.Attention, ReferenceDashboardAttention{
			Step:        3,
			Description: "waiting on assertion extraction",
			Count:       needsExtract,
		})
	}
	if needsReview > 0 {
		d.Attention = append(d.Attention, ReferenceDashboardAttention{
			Step:        4,
			Description: "waiting on assertion review + import",
			Count:       needsReview,
		})
	}
	return d, nil
}

// intOrZero converts a nullable SUM result to int. Zero rows in
// the source table return NULL from SUM; both Go's sql layer and
// SQLite emit that as any = nil, which int64/int scans
// reject. Falling back to zero matches the semantic ("no rows to
// count" = "zero completions").
func intOrZero(v any) int {
	switch n := v.(type) {
	case nil:
		return 0
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}
