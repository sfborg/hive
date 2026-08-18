package hive

import (
	"context"
	"fmt"

	"github.com/gnames/gnlib/ent/nomcode"
	"github.com/google/uuid"
	"github.com/sfborg/sflib/pkg/coldp"
)

// FixtureCase is one data scenario for a rule. Setup inserts the
// row(s) that define the scenario into a fresh archive. Bad cases
// are expected to trigger their parent rule; Good cases are
// expected to NOT trigger it (controls that prove the rule
// discriminates).
type FixtureCase struct {
	Note  string
	Setup func(context.Context, *Archive) error
}

// RuleFixture couples a rule id with the fixture data that
// exercises it — canonical Bad case(s) that must fire the rule and
// optional Good case(s) that must not. Both slices support
// extension over time as edge cases are discovered.
//
// Two consumers:
//
//   - pkg tests iterate every fixture, applying each case to its
//     own fresh archive and asserting the fire / no-fire invariant.
//   - tools/mkdemo applies every fixture (bad + good) to demo.db so
//     developers can inspect a canonical archive where every rule
//     has representative data.
type RuleFixture struct {
	RuleID      string
	Description string
	Bad         []FixtureCase
	Good        []FixtureCase
}

// RuleFixtures is the canonical registry — every rule hive ships
// with should have an entry here. Order is loose; grouped by
// scoping (role-table check-digit family, then name-scoped rules,
// then taxon-scoped, then reference-scoped).
var RuleFixtures = buildRuleFixtures()

func buildRuleFixtures() []RuleFixture {
	var out []RuleFixture
	out = append(out, roleTableFixtures()...)
	out = append(out, nameShapeFixtures()...)
	out = append(out, taxonFixtures()...)
	out = append(out, referenceFixtures()...)
	out = append(out, miscFixtures()...)
	return out
}

// roleTableFixtures produces the 15 role-table rules (5 tables ×
// {orcid, rorid, email}). Each rule gets one Bad + one Good case;
// good cases use canonical real identifiers.
func roleTableFixtures() []RuleFixture {
	// (real ORCID: 0000-0002-1825-0097 for Josiah Carberry)
	const goodORCID = "0000-0002-1825-0097"
	// transposed digits in the payload → check digit no longer matches
	const badORCID = "0000-0002-1852-0097"
	// real RORID: MIT is 05dxps055
	const goodRORID = "05dxps055"
	// off-by-one in the check digit
	const badRORID = "05dxps054"
	const goodEmail = "alice@example.com"
	const badEmail = "not an email"

	// insertRow builds a role-table INSERT that always populates
	// col__given / col__family (NOT NULL on every role table) plus
	// col__email (NOT NULL on contact). The custom column being
	// exercised gets its value from val; the email fallback is
	// non-empty when the tested column IS email — a clean default
	// address (never triggers the format rule) otherwise.
	insertRow := func(table, col, val string) func(context.Context, *Archive) error {
		return func(ctx context.Context, a *Archive) error {
			email := goodEmail
			if col == "col__email" {
				email = val
			}
			cols := "col__given, col__family, col__email"
			args := []interface{}{"Test", "Row", email}
			if col != "col__email" {
				cols += ", " + col
				args = append(args, val)
			}
			q := "INSERT INTO " + table + " (" + cols + ") VALUES (?, ?, ?" + optionalPlaceholder(col) + ")"
			_, err := a.db.ExecContext(ctx, q, args...)
			return err
		}
	}

	var out []RuleFixture
	for _, tbl := range []string{"creator", "contact", "contributor", "editor", "publisher"} {
		out = append(out,
			RuleFixture{
				RuleID:      "hive_" + tbl + "_orcid_check_digit",
				Description: "ORCID check digit invalid on " + tbl,
				Bad: []FixtureCase{
					{Note: "transposed digits", Setup: insertRow(tbl, "col__orcid", badORCID)},
				},
				Good: []FixtureCase{
					{Note: "canonical ORCID", Setup: insertRow(tbl, "col__orcid", goodORCID)},
				},
			},
			RuleFixture{
				RuleID:      "hive_" + tbl + "_rorid_check_digit",
				Description: "RORID check digit invalid on " + tbl,
				Bad: []FixtureCase{
					{Note: "off-by-one check digit", Setup: insertRow(tbl, "col__rorid", badRORID)},
				},
				Good: []FixtureCase{
					{Note: "canonical RORID", Setup: insertRow(tbl, "col__rorid", goodRORID)},
				},
			},
			RuleFixture{
				RuleID:      "hive_" + tbl + "_email_format",
				Description: "Email format looks unusual on " + tbl,
				Bad: []FixtureCase{
					{Note: "not-an-email string", Setup: insertRow(tbl, "col__email", badEmail)},
				},
				Good: []FixtureCase{
					{Note: "canonical email", Setup: insertRow(tbl, "col__email", goodEmail)},
				},
			},
		)
	}
	return out
}

// nameShapeFixtures — the parsed-name-shape family from CoL.
func nameShapeFixtures() []RuleFixture {
	// Insert a name row directly so we control every column
	// precisely. Setup helper for hand-crafted name rows.
	insertName := func(fields map[string]string) func(context.Context, *Archive) error {
		return func(ctx context.Context, a *Archive) error {
			cols := []string{"col__id", "col__scientific_name", "gn__scientific_name_string"}
			vals := []interface{}{}
			if _, ok := fields["col__id"]; !ok {
				fields["col__id"] = randomID()
			}
			if _, ok := fields["gn__scientific_name_string"]; !ok {
				fields["gn__scientific_name_string"] = fields["col__scientific_name"]
			}
			for _, c := range cols {
				vals = append(vals, fields[c])
			}
			extraCols := []string{}
			for k := range fields {
				if k == "col__id" || k == "col__scientific_name" || k == "gn__scientific_name_string" {
					continue
				}
				extraCols = append(extraCols, k)
			}
			for _, c := range extraCols {
				cols = append(cols, c)
				vals = append(vals, fields[c])
			}
			placeholders := ""
			for i := range cols {
				if i > 0 {
					placeholders += ", "
				}
				placeholders += "?"
			}
			colList := ""
			for i, c := range cols {
				if i > 0 {
					colList += ", "
				}
				colList += c
			}
			q := "INSERT INTO name (" + colList + ") VALUES (" + placeholders + ")"
			_, err := a.db.ExecContext(ctx, q, vals...)
			return err
		}
	}
	return []RuleFixture{
		{
			RuleID:      "clb_wrong_monomial_case_uninomial",
			Description: "Uninomial not in title case",
			Bad: []FixtureCase{
				{Note: "all lowercase", Setup: insertName(map[string]string{
					"col__scientific_name": "panthera",
					"col__uninomial":       "panthera",
				})},
				{Note: "all uppercase", Setup: insertName(map[string]string{
					"col__scientific_name": "PANTHERA",
					"col__uninomial":       "PANTHERA",
				})},
			},
			Good: []FixtureCase{
				{Note: "canonical title case", Setup: insertName(map[string]string{
					"col__scientific_name": "Panthera",
					"col__uninomial":       "Panthera",
				})},
			},
		},
		{
			RuleID:      "clb_wrong_monomial_case_genus",
			Description: "Genus not in title case",
			Bad: []FixtureCase{
				{Note: "lowercase genus", Setup: insertName(map[string]string{
					"col__scientific_name": "panthera leo",
					"col__genus":           "panthera",
					"col__specific_epithet": "leo",
				})},
			},
			Good: []FixtureCase{
				{Note: "title-case genus", Setup: insertName(map[string]string{
					"col__scientific_name": "Panthera leo",
					"col__genus":           "Panthera",
					"col__specific_epithet": "leo",
				})},
			},
		},
		{
			RuleID:      "clb_uppercase_epithet_specific",
			Description: "Specific epithet not lowercase",
			Bad: []FixtureCase{
				{Note: "capitalized epithet", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera Leo",
					"col__genus":            "Panthera",
					"col__specific_epithet": "Leo",
				})},
			},
			Good: []FixtureCase{
				{Note: "lowercase epithet", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera leo",
					"col__genus":            "Panthera",
					"col__specific_epithet": "leo",
				})},
			},
		},
		{
			RuleID:      "clb_uppercase_epithet_infraspecific",
			Description: "Infraspecific epithet not lowercase",
			Bad: []FixtureCase{
				{Note: "capitalized infraspecific", Setup: insertName(map[string]string{
					"col__scientific_name":        "Panthera leo Persica",
					"col__genus":                  "Panthera",
					"col__specific_epithet":       "leo",
					"col__infraspecific_epithet":  "Persica",
				})},
			},
			Good: []FixtureCase{
				{Note: "lowercase infraspecific", Setup: insertName(map[string]string{
					"col__scientific_name":        "Panthera leo persica",
					"col__genus":                  "Panthera",
					"col__specific_epithet":       "leo",
					"col__infraspecific_epithet":  "persica",
				})},
			},
		},
		{
			RuleID:      "clb_multi_word_monomial_uninomial",
			Description: "Uninomial has multiple words",
			Bad: []FixtureCase{
				{Note: "space in uninomial", Setup: insertName(map[string]string{
					"col__scientific_name": "Two Words",
					"col__uninomial":       "Two Words",
				})},
			},
			Good: []FixtureCase{
				{Note: "single word", Setup: insertName(map[string]string{
					"col__scientific_name": "Panthera",
					"col__uninomial":       "Panthera",
				})},
			},
		},
		{
			RuleID:      "clb_multi_word_epithet_specific",
			Description: "Specific epithet has multiple words",
			Bad: []FixtureCase{
				{Note: "space in specific epithet", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera leo persica",
					"col__genus":            "Panthera",
					"col__specific_epithet": "leo persica",
				})},
			},
			Good: []FixtureCase{
				{Note: "single-word specific epithet", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera leo",
					"col__genus":            "Panthera",
					"col__specific_epithet": "leo",
				})},
			},
		},
		{
			RuleID:      "clb_missing_genus",
			Description: "Specific epithet present but genus empty",
			Bad: []FixtureCase{
				{Note: "epithet without genus", Setup: insertName(map[string]string{
					"col__scientific_name":  "leo",
					"col__specific_epithet": "leo",
				})},
			},
			Good: []FixtureCase{
				{Note: "genus and epithet both set", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera leo",
					"col__genus":            "Panthera",
					"col__specific_epithet": "leo",
				})},
			},
		},
		{
			RuleID:      "clb_infraspecific_needs_specific",
			Description: "Infraspecific epithet present but specific empty",
			Bad: []FixtureCase{
				{Note: "infraspecific without specific", Setup: insertName(map[string]string{
					"col__scientific_name":       "persica",
					"col__genus":                 "Panthera",
					"col__infraspecific_epithet": "persica",
				})},
			},
			Good: []FixtureCase{
				{Note: "both specific and infraspecific set", Setup: insertName(map[string]string{
					"col__scientific_name":       "Panthera leo persica",
					"col__genus":                 "Panthera",
					"col__specific_epithet":      "leo",
					"col__infraspecific_epithet": "persica",
				})},
			},
		},
		{
			RuleID:      "clb_species_rank_needs_specific",
			Description: "Rank is SPECIES-group but specific_epithet empty",
			Bad: []FixtureCase{
				{Note: "SPECIES rank without epithet", Setup: insertName(map[string]string{
					"col__scientific_name": "Panthera",
					"col__genus":           "Panthera",
					"col__rank_id":         "SPECIES",
				})},
			},
			Good: []FixtureCase{
				{Note: "SPECIES rank with epithet", Setup: insertName(map[string]string{
					"col__scientific_name":  "Panthera leo",
					"col__genus":            "Panthera",
					"col__specific_epithet": "leo",
					"col__rank_id":          "SPECIES",
				})},
			},
		},
		{
			RuleID:      "clb_suprageneric_needs_uninomial",
			Description: "Suprageneric rank but uninomial empty",
			Bad: []FixtureCase{
				{Note: "FAMILY rank without uninomial", Setup: insertName(map[string]string{
					"col__scientific_name": "some family",
					"col__rank_id":         "FAMILY",
				})},
			},
			Good: []FixtureCase{
				{Note: "FAMILY rank with uninomial", Setup: insertName(map[string]string{
					"col__scientific_name": "Felidae",
					"col__uninomial":       "Felidae",
					"col__rank_id":         "FAMILY",
				})},
			},
		},
		{
			RuleID:      "clb_unlikely_year",
			Description: "Publication year outside plausible range",
			Bad: []FixtureCase{
				{Note: "year 1000 (pre-Linnaean)", Setup: insertName(map[string]string{
					"col__scientific_name":     "Panthera antiqua",
					"col__genus":               "Panthera",
					"col__specific_epithet":    "antiqua",
					"col__published_in_year":   "1000",
				})},
			},
			Good: []FixtureCase{
				{Note: "year 1758", Setup: insertName(map[string]string{
					"col__scientific_name":     "Panthera leo",
					"col__genus":               "Panthera",
					"col__specific_epithet":    "leo",
					"col__published_in_year":   "1758",
				})},
			},
		},
	}
}

// taxonFixtures — rules scoped on the taxon table.
func taxonFixtures() []RuleFixture {
	return []RuleFixture{
		{
			RuleID:      "hive_taxon_parent_cycle",
			Description: "Taxon on parent-chain cycle",
			Bad: []FixtureCase{
				{Note: "3-taxon cycle formed via direct SQL", Setup: buildCycleFixture},
			},
			// Good: any tree without cycles. Empty archive is a good control.
		},
		{
			RuleID:      "clb_parent_genus_missing",
			Description: "Species has no genus ancestor",
			Bad: []FixtureCase{
				{Note: "species placed directly under family", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nFam, err := tx.CreateName(coldp.Name{ScientificName: "Felidae Fischer, 1817", Rank: ParseRank("family")})
						if err != nil {
							return err
						}
						nSp, err := tx.CreateName(coldp.Name{ScientificName: "Wandera bar Author, 2020", Rank: ParseRank("species")})
						if err != nil {
							return err
						}
						tFam, err := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
						if err != nil {
							return err
						}
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tFam})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "species properly under genus", Setup: buildFamilyGenusSpecies},
			},
		},
		{
			RuleID:      "clb_parent_species_missing",
			Description: "Infraspecific taxon has no species ancestor",
			Bad: []FixtureCase{
				{Note: "subspecies under genus (no species)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nFam, err := tx.CreateName(coldp.Name{ScientificName: "Felidae Fischer, 1817", Rank: ParseRank("family")})
						if err != nil {
							return err
						}
						nGen, err := tx.CreateName(coldp.Name{ScientificName: "Panthera Oken, 1816", Rank: ParseRank("genus")})
						if err != nil {
							return err
						}
						nSub, err := tx.CreateName(coldp.Name{ScientificName: "Panthera leo persica", Rank: ParseRank("subspecies")})
						if err != nil {
							return err
						}
						tFam, _ := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
						tGen, _ := tx.CreateTaxon(coldp.Taxon{NameID: nGen, ParentID: tFam})
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSub, ParentID: tGen})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "subspecies under species", Setup: buildFamilyGenusSpeciesSubspecies},
			},
		},
		{
			RuleID:      "clb_classification_rank_order_invalid",
			Description: "Parent rank not higher than child",
			Bad: []FixtureCase{
				{Note: "order under genus (rank inversion)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nGen, err := tx.CreateName(coldp.Name{ScientificName: "Foobar Author, 1900", Rank: ParseRank("genus"), Code: nomcode.New("iczn")})
						if err != nil {
							return err
						}
						nOrd, err := tx.CreateName(coldp.Name{ScientificName: "Foobarales Author, 1901", Rank: ParseRank("order"), Code: nomcode.New("iczn")})
						if err != nil {
							return err
						}
						tGen, _ := tx.CreateTaxon(coldp.Taxon{NameID: nGen})
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nOrd, ParentID: tGen})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "species under genus", Setup: buildFamilyGenusSpecies},
			},
		},
		{
			RuleID:      "clb_published_before_genus",
			Description: "Species year predates parent genus year",
			Bad: []FixtureCase{
				{Note: "species 1850 under genus 1900", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nFam, err := tx.CreateName(coldp.Name{ScientificName: "Felidae Fischer, 1817", Rank: ParseRank("family")})
						if err != nil {
							return err
						}
						nGen, err := tx.CreateName(coldp.Name{ScientificName: "Panthera Author, 1900", Rank: ParseRank("genus"), PublishedInYear: "1900"})
						if err != nil {
							return err
						}
						nSp, err := tx.CreateName(coldp.Name{ScientificName: "Panthera prior Author, 1850", Rank: ParseRank("species"), PublishedInYear: "1850"})
						if err != nil {
							return err
						}
						tFam, _ := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
						tGen, _ := tx.CreateTaxon(coldp.Taxon{NameID: nGen, ParentID: tFam})
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tGen})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "species 1950 under genus 1900", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nFam, err := tx.CreateName(coldp.Name{ScientificName: "Felidae Fischer, 1817", Rank: ParseRank("family")})
						if err != nil {
							return err
						}
						nGen, err := tx.CreateName(coldp.Name{ScientificName: "Panthera Author, 1900", Rank: ParseRank("genus"), PublishedInYear: "1900"})
						if err != nil {
							return err
						}
						nSp, err := tx.CreateName(coldp.Name{ScientificName: "Panthera later Author, 1950", Rank: ParseRank("species"), PublishedInYear: "1950"})
						if err != nil {
							return err
						}
						tFam, _ := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
						tGen, _ := tx.CreateTaxon(coldp.Taxon{NameID: nGen, ParentID: tFam})
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tGen})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "hive_probable_incertae_sedis",
			Description: "ICZN species under non-genus parent",
			Bad: []FixtureCase{
				{Note: "zoological species under family", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						nFam, err := tx.CreateName(coldp.Name{ScientificName: "Felidae Fischer, 1817", Rank: ParseRank("family"), Code: nomcode.New("iczn")})
						if err != nil {
							return err
						}
						nSp, err := tx.CreateName(coldp.Name{ScientificName: "Kroniqus baz Author, 2020", Rank: ParseRank("species"), Code: nomcode.New("iczn")})
						if err != nil {
							return err
						}
						tFam, _ := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
						_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tFam})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "zoological species under genus", Setup: buildFamilyGenusSpecies},
			},
		},
	}
}

// referenceFixtures — rules scoped on reference.
func referenceFixtures() []RuleFixture {
	insertRef := func(fields map[string]string) func(context.Context, *Archive) error {
		return func(ctx context.Context, a *Archive) error {
			if _, ok := fields["col__id"]; !ok {
				fields["col__id"] = randomID()
			}
			var cols []string
			var vals []interface{}
			for k, v := range fields {
				cols = append(cols, k)
				vals = append(vals, v)
			}
			placeholders := ""
			for i := range cols {
				if i > 0 {
					placeholders += ", "
				}
				placeholders += "?"
			}
			colList := ""
			for i, c := range cols {
				if i > 0 {
					colList += ", "
				}
				colList += c
			}
			// Disable FK so nullable FKs defaulting to '' don't fail.
			if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
				return err
			}
			_, err := a.db.ExecContext(ctx, "INSERT INTO reference ("+colList+") VALUES ("+placeholders+")", vals...)
			a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			return err
		}
	}
	return []RuleFixture{
		{
			RuleID:      "hive_reference_isbn_check_digit",
			Description: "ISBN check digit invalid",
			Bad: []FixtureCase{
				{Note: "transposed digits in ISBN-10", Setup: insertRef(map[string]string{"col__isbn": "0306460152"})},
			},
			Good: []FixtureCase{
				{Note: "canonical ISBN-10 (0-306-40615-2)", Setup: insertRef(map[string]string{"col__isbn": "0306406152"})},
			},
		},
		{
			RuleID:      "hive_reference_issn_check_digit",
			Description: "ISSN check digit invalid",
			Bad: []FixtureCase{
				{Note: "transposed digits", Setup: insertRef(map[string]string{"col__issn": "2049-3603"})},
			},
			Good: []FixtureCase{
				{Note: "canonical ISSN (Nature 2049-3630)", Setup: insertRef(map[string]string{"col__issn": "2049-3630"})},
			},
		},
		{
			RuleID:      "clb_doi_invalid",
			Description: "DOI doesn't match the expected format",
			Bad: []FixtureCase{
				{Note: "missing 10. prefix", Setup: insertRef(map[string]string{"col__doi": "abc.def/xyz"})},
			},
			Good: []FixtureCase{
				{Note: "canonical DOI form", Setup: insertRef(map[string]string{"col__doi": "10.1234/abcd"})},
			},
		},
		{
			RuleID:      "clb_reference_id_invalid",
			Description: "Name references nonexistent reference id",
			Bad: []FixtureCase{
				{Note: "dangling FK inserted with foreign_keys OFF", Setup: func(ctx context.Context, a *Archive) error {
					var nameID string
					if err := a.WithTx(ctx, func(tx *Tx) error {
						id, err := tx.CreateName(coldp.Name{ScientificName: "Rangi bar Author, 2020"})
						if err != nil {
							return err
						}
						nameID = id
						return nil
					}); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx, `UPDATE name SET col__reference_id = ? WHERE col__id = ?`, "ref-nonexistent", nameID); err != nil {
						return err
					}
					_, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
					return err
				}},
			},
			Good: []FixtureCase{
				{Note: "name with empty reference_id", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{ScientificName: "Ronjo foo Author, 2020"})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "hive_source_year_mismatch",
			Description: "Name's published_in_year doesn't match reference's year",
			Bad: []FixtureCase{
				{Note: "name says 1799, reference says 1758", Setup: func(ctx context.Context, a *Archive) error {
					refID := "ref-mismatch-" + randomID()[:8]
					if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx,
						`INSERT INTO reference (col__id, col__issued) VALUES (?, ?)`, refID, "1758"); err != nil {
						return err
					}
					a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName:  "Panfio bispo Author, 1799",
							ReferenceID:     refID,
							PublishedInYear: "1799",
						})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "year matches reference", Setup: func(ctx context.Context, a *Archive) error {
					refID := "ref-match-" + randomID()[:8]
					if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx,
						`INSERT INTO reference (col__id, col__issued) VALUES (?, ?)`, refID, "1758"); err != nil {
						return err
					}
					a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName:  "Panzz leo Linnaeus, 1758",
							ReferenceID:     refID,
							PublishedInYear: "1758",
						})
						return err
					})
				}},
			},
		},
	}
}

// miscFixtures — rules that don't cleanly group above.
func miscFixtures() []RuleFixture {
	return []RuleFixture{
		{
			RuleID:      "hive_specific_epithet_on_suprageneric",
			Description: "Suprageneric rank with specific_epithet set",
			Bad: []FixtureCase{
				{Note: "INFRAORDER rank with specific_epithet", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName:   "Baru molens Author, 2020",
							Rank:             ParseRank("infraorder"),
							SpecificEpithet:  "molens",
						})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "SPECIES rank with specific_epithet", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName:   "Panthera leo",
							Rank:             ParseRank("species"),
							Genus:            "Panthera",
							SpecificEpithet:  "leo",
						})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "hive_parse_failed",
			Description: "gnparser could not parse the name (quality 0)",
			Bad: []FixtureCase{
				{Note: "force parse_quality=0 via direct SQL", Setup: func(ctx context.Context, a *Archive) error {
					id := randomID()
					if _, err := a.db.ExecContext(ctx,
						`INSERT INTO name (col__id, col__scientific_name, gn__scientific_name_string, gn__parse_quality)
						 VALUES (?, ?, ?, 0)`, id, "!!!not-a-name!!!", "!!!not-a-name!!!"); err != nil {
						return err
					}
					return nil
				}},
			},
			Good: []FixtureCase{
				{Note: "quality 1 via normal parse path", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{ScientificName: "Panthera leo"})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "hive_parse_warnings",
			Description: "gnparser recorded warnings (quality > 1)",
			Bad: []FixtureCase{
				{Note: "force parse_quality=4 via direct SQL", Setup: func(ctx context.Context, a *Archive) error {
					id := randomID()
					if _, err := a.db.ExecContext(ctx,
						`INSERT INTO name (col__id, col__scientific_name, gn__scientific_name_string, gn__parse_quality)
						 VALUES (?, ?, ?, 4)`, id, "Weird sp. Author?", "Weird sp. Author?"); err != nil {
						return err
					}
					return nil
				}},
			},
			Good: []FixtureCase{
				{Note: "clean parse (quality 1)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{ScientificName: "Panthera leo"})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "hive_name_on_self_relation",
			Description: "Name appears on both sides of a name_relation row",
			Bad: []FixtureCase{
				{Note: "self-referenced relation planted with foreign_keys OFF", Setup: func(ctx context.Context, a *Archive) error {
					var nameID string
					if err := a.WithTx(ctx, func(tx *Tx) error {
						id, err := tx.CreateName(coldp.Name{ScientificName: "Selfum referens Author, 2020"})
						if err != nil {
							return err
						}
						nameID = id
						return nil
					}); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
						return err
					}
					if _, err := a.db.ExecContext(ctx,
						`INSERT INTO name_relation (col__name_id, col__related_name_id, col__type_id) VALUES (?, ?, 'BASIONYM')`,
						nameID, nameID,
					); err != nil {
						return err
					}
					_, err := a.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
					return err
				}},
			},
		},
		{
			RuleID:      "hive_infraspecific_marker_missing",
			Description: "Infraspecific name lacks its rank marker in the scientific name string",
			Bad: []FixtureCase{
				{Note: "ICN variety without var.", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Quercus alba pinnatifida",
							Rank:           ParseRank("variety"),
							Code:           nomcode.New("icn"),
						})
						return err
					})
				}},
				{Note: "ICN form without f.", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Quercus alba pinnatifida",
							Rank:           ParseRank("form"),
							Code:           nomcode.New("icn"),
						})
						return err
					})
				}},
				{Note: "ICZN variety without var. (historical synonym)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Panthera leo persica",
							Rank:           ParseRank("variety"),
							Code:           nomcode.New("iczn"),
						})
						return err
					})
				}},
				{Note: "empty code + subspecies without subsp. (safe default)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Panthera leo persica",
							Rank:           ParseRank("subspecies"),
						})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "ICZN subspecies without marker (marker optional under ICZN)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Panthera leo persica",
							Rank:           ParseRank("subspecies"),
							Code:           nomcode.New("iczn"),
						})
						return err
					})
				}},
				{Note: "ICVCN skipped entirely", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Alphavirus subalphavirus alphagenus",
							Rank:           ParseRank("subspecies"),
							Code:           nomcode.New("icvcn"),
						})
						return err
					})
				}},
				{Note: "ICN variety with var. present", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Quercus alba var. pinnatifida",
							Rank:           ParseRank("variety"),
							Code:           nomcode.New("icn"),
						})
						return err
					})
				}},
				{Note: "species rank (not infraspecific)", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{
							ScientificName: "Panthera leo",
							Rank:           ParseRank("species"),
							Code:           nomcode.New("iczn"),
						})
						return err
					})
				}},
			},
		},
		{
			RuleID:      "clb_duplicate_name",
			Description: "Two name records share the same canonical form",
			Bad: []FixtureCase{
				{Note: "two identical scientific names", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						if _, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"}); err != nil {
							return err
						}
						_, err := tx.CreateName(coldp.Name{ScientificName: "Foo bar"})
						return err
					})
				}},
			},
			Good: []FixtureCase{
				{Note: "single name — no duplicate possible", Setup: func(ctx context.Context, a *Archive) error {
					return a.WithTx(ctx, func(tx *Tx) error {
						_, err := tx.CreateName(coldp.Name{ScientificName: "Uniqueus onlyus"})
						return err
					})
				}},
			},
		},
	}
}

// buildFamilyGenusSpecies is a Good-case helper: builds a valid
// Family → Genus → Species tree that satisfies most taxon rules.
func buildFamilyGenusSpecies(ctx context.Context, a *Archive) error {
	return a.WithTx(ctx, func(tx *Tx) error {
		nFam, err := tx.CreateName(coldp.Name{ScientificName: "Wellformidae Author, 1900", Rank: ParseRank("family")})
		if err != nil {
			return err
		}
		nGen, err := tx.CreateName(coldp.Name{ScientificName: "Wellformus Author, 1900", Rank: ParseRank("genus")})
		if err != nil {
			return err
		}
		nSp, err := tx.CreateName(coldp.Name{ScientificName: "Wellformus goodus Author, 1950", Rank: ParseRank("species")})
		if err != nil {
			return err
		}
		tFam, err := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
		if err != nil {
			return err
		}
		tGen, err := tx.CreateTaxon(coldp.Taxon{NameID: nGen, ParentID: tFam})
		if err != nil {
			return err
		}
		_, err = tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tGen})
		return err
	})
}

func buildFamilyGenusSpeciesSubspecies(ctx context.Context, a *Archive) error {
	return a.WithTx(ctx, func(tx *Tx) error {
		nFam, _ := tx.CreateName(coldp.Name{ScientificName: "Ubfamidae Author, 1900", Rank: ParseRank("family")})
		nGen, _ := tx.CreateName(coldp.Name{ScientificName: "Ubgenus Author, 1900", Rank: ParseRank("genus")})
		nSp, _ := tx.CreateName(coldp.Name{ScientificName: "Ubgenus ubspecies Author, 1950", Rank: ParseRank("species")})
		nSub, _ := tx.CreateName(coldp.Name{ScientificName: "Ubgenus ubspecies subgood Author, 1960", Rank: ParseRank("subspecies")})
		tFam, _ := tx.CreateTaxon(coldp.Taxon{NameID: nFam})
		tGen, _ := tx.CreateTaxon(coldp.Taxon{NameID: nGen, ParentID: tFam})
		tSp, _ := tx.CreateTaxon(coldp.Taxon{NameID: nSp, ParentID: tGen})
		_, err := tx.CreateTaxon(coldp.Taxon{NameID: nSub, ParentID: tSp})
		return err
	})
}

// buildCycleFixture creates 3 taxa and stitches a cycle via direct
// SQL, bypassing MoveTaxon's cycle guard.
func buildCycleFixture(ctx context.Context, a *Archive) error {
	var taxonIDs [3]string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		for i := 0; i < 3; i++ {
			n, err := tx.CreateName(coldp.Name{ScientificName: fmt.Sprintf("Cyclus taxonus%d Author, 2020", i)})
			if err != nil {
				return err
			}
			id, err := tx.CreateTaxon(coldp.Taxon{NameID: n})
			if err != nil {
				return err
			}
			taxonIDs[i] = id
		}
		return nil
	}); err != nil {
		return err
	}
	// Stitch A → B → C → A by direct SQL.
	for i, id := range taxonIDs {
		parent := taxonIDs[(i+1)%3]
		if _, err := a.db.ExecContext(ctx,
			`UPDATE taxon SET col__parent_id = ? WHERE col__id = ?`, parent, id); err != nil {
			return err
		}
	}
	return nil
}

// randomID returns a fresh UUID-shaped string for direct-SQL
// inserts that need a col__id value.
func randomID() string {
	return uuid.NewString()
}

// optionalPlaceholder returns ", ?" when col is a role-table
// column other than col__email — so the insertRow helper can
// omit the trailing placeholder when the exercised column IS
// email (already covered by the fixed three columns).
func optionalPlaceholder(col string) string {
	if col == "col__email" {
		return ""
	}
	return ", ?"
}
