package hive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DataFixtureRow is one row a data-driven fixture case inserts: a
// table name and its column values.
type DataFixtureRow struct {
	Table  string         `json:"table"`
	Values map[string]any `json:"values"`
}

type dataFixtureCase struct {
	Note string           `json:"note"`
	Rows []DataFixtureRow `json:"rows"`
}

type dataFixture struct {
	RuleID      string            `json:"rule_id"`
	Description string            `json:"description"`
	Schema      string            `json:"schema"`
	Bad         []dataFixtureCase `json:"bad"`
	Good        []dataFixtureCase `json:"good"`
}

// dataFixtureTables lists the tables data-driven fixtures may insert
// into.
var dataFixtureTables = map[string]bool{
	"name": true, "taxon": true, "synonym": true, "name_relation": true,
	"type_material": true, "reference": true,
	"tw__otu": true, "tw__term": true, "tw__annotation": true,
}

var dataFixtureColumn = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// RuleFixturesFromJSON decodes rule fixtures expressed as data: for
// each rule, Bad and Good cases that list the rows to insert. Used
// for the TaxonWorks-derived bundles, whose fixtures are generated
// alongside the rules (pkg/testdata/tw_rulefixtures.json).
func RuleFixturesFromJSON(data []byte) ([]RuleFixture, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw []dataFixture
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("core: decode rule fixtures: %w", err)
	}
	out := make([]RuleFixture, 0, len(raw))
	for _, f := range raw {
		bad, err := dataFixtureCases(f.RuleID, f.Bad)
		if err != nil {
			return nil, err
		}
		good, err := dataFixtureCases(f.RuleID, f.Good)
		if err != nil {
			return nil, err
		}
		out = append(out, RuleFixture{
			RuleID:      f.RuleID,
			Description: f.Description,
			Schema:      f.Schema,
			Bad:         bad,
			Good:        good,
		})
	}
	return out, nil
}

func dataFixtureCases(ruleID string, cases []dataFixtureCase) ([]FixtureCase, error) {
	out := make([]FixtureCase, 0, len(cases))
	for _, c := range cases {
		for _, r := range c.Rows {
			if !dataFixtureTables[r.Table] {
				return nil, fmt.Errorf("core: fixture %s: table %q not allowed", ruleID, r.Table)
			}
			for col := range r.Values {
				if !dataFixtureColumn.MatchString(col) {
					return nil, fmt.Errorf("core: fixture %s: bad column %q", ruleID, col)
				}
			}
		}
		out = append(out, FixtureCase{Note: c.Note, Setup: insertDataFixtureRows(c.Rows)})
	}
	return out, nil
}

// insertDataFixtureRows inserts the rows in order. Table and column
// names were checked when the fixtures were decoded. Fixture rows
// carry only the columns a rule reads, so the inserts run on one
// connection with foreign keys off (PRAGMA foreign_keys is
// per-connection).
func insertDataFixtureRows(rows []DataFixtureRow) func(context.Context, *Archive) error {
	return func(ctx context.Context, a *Archive) error {
		conn, err := a.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
		for _, r := range rows {
			cols := make([]string, 0, len(r.Values))
			for col := range r.Values {
				cols = append(cols, col)
			}
			sort.Strings(cols)
			vals := make([]any, len(cols))
			for i, col := range cols {
				vals[i] = dataFixtureValue(r.Values[col])
			}
			q := "INSERT INTO " + r.Table + " (" + strings.Join(cols, ", ") +
				") VALUES (" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"
			if _, err := conn.ExecContext(ctx, q, vals...); err != nil {
				return fmt.Errorf("insert %s: %w", r.Table, err)
			}
		}
		return nil
	}
}

// dataFixtureValue turns a decoded JSON number into an int64 when it
// is integral, so STRICT INTEGER columns accept it.
func dataFixtureValue(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}
