package hive

import (
	"context"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

func TestTaxonTable(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "0000-0000-0000-0000")
	var genus, species, synName string
	if err := a.WithTx(ctx, func(tx *Tx) error {
		var err error
		if genus, err = tx.CreateTaxon(taxonForTest(insertTestName(t, tx, "Philaenus"))); err != nil {
			return err
		}
		sp := taxonForTest(insertTestName(t, tx, "Philaenus spumarius"))
		sp.ParentID = genus
		if species, err = tx.CreateTaxon(sp); err != nil {
			return err
		}
		synName = insertTestName(t, tx, "Cercopis spumaria")
		if _, err = tx.AddSynonym(coldp.Synonym{TaxonID: species, NameID: synName}); err != nil {
			return err
		}
		for _, v := range []coldp.Vernacular{
			{TaxonID: species, Name: "meadow spittlebug", Language: "eng"},
			{TaxonID: species, Name: "meadow spittlebug", Language: "eng"},
			{TaxonID: species, Name: "Wiesen-Schaumzikade", Language: "deu"},
		} {
			if _, err := tx.AddVernacular(v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := a.db.Exec(`INSERT INTO __gsvalidator_results (table_name, record_id, rule_id, rule_name, field_name, severity, enforcement, message, created_at, is_resolved)
		VALUES ('name', ?, 'r1', 'Missing etymology', '', 'warn', 'soft', 'm', '2026', 0)`, synName); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	rows, total, err := a.TaxonTable(ctx, TaxonTableQuery{})
	if err != nil || total != 3 || len(rows) != 3 {
		t.Fatalf("all rows: %d of %d, %v", len(rows), total, err)
	}
	var syn, acc *TaxonTableRow
	for i := range rows {
		switch rows[i].Name {
		case "Cercopis spumaria":
			syn = &rows[i]
		case "Philaenus spumarius":
			acc = &rows[i]
		}
	}
	if syn == nil || syn.Usage != "synonym" || syn.TaxonID != species || syn.Related != "Philaenus spumarius" {
		t.Errorf("synonym row = %+v", syn)
	}
	if acc == nil || acc.Related != "Philaenus" || acc.Synonyms != 1 || len(acc.Vernaculars) != 2 {
		t.Errorf("accepted row = %+v", acc)
	}
	if syn != nil && (syn.IssuesBySeverity["warn"] != 1 || len(syn.IssueRules) != 1) {
		t.Errorf("synonym issues = %v %v", syn.IssuesBySeverity, syn.IssueRules)
	}

	rows, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Q: "spum", Usage: "accepted"})
	if total != 1 || rows[0].Name != "Philaenus spumarius" {
		t.Errorf("filtered = %d rows", total)
	}
	rows, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Issue: "rule:r1"})
	if total != 1 || rows[0].Name != "Cercopis spumaria" {
		t.Errorf("issue filter = %d rows", total)
	}
	sort, err := ParseTaxonTableSort("vernaculars:desc")
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	rows, _, _ = a.TaxonTable(ctx, TaxonTableQuery{Sort: sort})
	if rows[0].Name != "Philaenus spumarius" {
		t.Errorf("sort by vernaculars: first = %s", rows[0].Name)
	}
	// Within the genus: the genus, the species and its synonym.
	_, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Within: genus})
	if total != 3 {
		t.Errorf("within genus = %d rows, want 3", total)
	}
	_, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Within: species})
	if total != 2 {
		t.Errorf("within species = %d rows, want 2", total)
	}
	// Accepted taxa missing vernacular names: only the genus. The
	// synonym row neither has nor misses taxon-level data.
	rows, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Missing: "vernaculars"})
	if total != 1 || rows[0].Name != "Philaenus" {
		t.Errorf("missing vernaculars = %d rows", total)
	}
	_, total, _ = a.TaxonTable(ctx, TaxonTableQuery{Has: "synonyms"})
	if total != 1 {
		t.Errorf("has synonyms = %d rows, want 1", total)
	}
	if _, err := ParseTaxonTableSort("nope:asc"); err == nil {
		t.Error("unknown sort column accepted")
	}
}
