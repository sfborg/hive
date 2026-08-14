package hive

import (
	"context"
	"testing"

	"github.com/sfborg/sflib/pkg/coldp"
)

// TestNomenclaturalHistoryAcceptedCluster — a taxon whose accepted
// name has an explicit basionym produces one "accepted" cluster
// containing basionym + accepted, basionym flagged first.
func TestNomenclaturalHistoryAcceptedCluster(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var (
		taxonID       string
		basionymName  string
		acceptedName  string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		// Basionym: Felis leo (Linnaeus, 1758)
		basionymName = insertTestName(t, tx, "Felis leo")
		if _, err := tx.tx.ExecContext(tx.ctx, `UPDATE name SET
			gn__canonical_simple = ?, col__authorship = ?,
			col__combination_authorship_year = ?
			WHERE col__id = ?`,
			"Felis leo", "Linnaeus, 1758", "1758", basionymName,
		); err != nil {
			return err
		}
		// Current combination: Panthera leo (Linnaeus, 1758)
		acceptedName = insertTestName(t, tx, "Panthera leo")
		if _, err := tx.tx.ExecContext(tx.ctx, `UPDATE name SET
			gn__canonical_simple = ?, col__authorship = ?,
			col__combination_authorship_year = ?
			WHERE col__id = ?`,
			"Panthera leo", "(Linnaeus, 1758)", "1758", acceptedName,
		); err != nil {
			return err
		}
		id, err := tx.CreateTaxon(taxonForTest(acceptedName))
		if err != nil {
			return err
		}
		taxonID = id
		// Panthera leo → BASIONYM → Felis leo
		return tx.LinkNameRelation(coldp.NameRelation{
			NameID:        acceptedName,
			RelatedNameID: basionymName,
			Type:          coldp.Basionym,
		})
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hist, err := a.NomenclaturalHistory(ctx, taxonID)
	if err != nil {
		t.Fatalf("NomenclaturalHistory: %v", err)
	}
	if len(hist.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(hist.Clusters))
	}
	c := hist.Clusters[0]
	if c.Role != "accepted" {
		t.Errorf("cluster role = %q, want %q", c.Role, "accepted")
	}
	if len(c.Names) != 2 {
		t.Fatalf("expected 2 names in cluster, got %d: %+v", len(c.Names), c.Names)
	}
	// Basionym first.
	if !c.Names[0].IsBasionym || c.Names[0].NameID != basionymName {
		t.Errorf("first name should be the basionym; got %+v", c.Names[0])
	}
	if c.Names[0].Involvement != "unlinked" {
		t.Errorf("basionym involvement = %q; expected 'unlinked' (basionym is not itself the accepted name here)",
			c.Names[0].Involvement)
	}
	// Accepted second.
	if c.Names[1].IsBasionym || c.Names[1].NameID != acceptedName {
		t.Errorf("second name should be the accepted (non-basionym); got %+v", c.Names[1])
	}
	if c.Names[1].Involvement != "accepted" {
		t.Errorf("accepted involvement = %q; expected 'accepted'", c.Names[1].Involvement)
	}
}

// TestNomenclaturalHistorySynonymClustersSeparately — a synonym
// with its own distinct basionym gets its own cluster, and the
// synonym-role cluster carries the synonym-link id on the involved
// name.
func TestNomenclaturalHistorySynonymClustersSeparately(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var (
		taxonID, acceptedName, synName, synBasName, synonymID string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		acceptedName = insertTestName(t, tx, "Panthera leo")
		id, err := tx.CreateTaxon(taxonForTest(acceptedName))
		if err != nil {
			return err
		}
		taxonID = id
		// Synonym's own basionym family (unrelated to accepted's).
		synBasName = insertTestName(t, tx, "Leo persicus")
		synName = insertTestName(t, tx, "Panthera leo persica")
		if err := tx.LinkNameRelation(coldp.NameRelation{
			NameID:        synName,
			RelatedNameID: synBasName,
			Type:          coldp.Basionym,
		}); err != nil {
			return err
		}
		sid, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  synName,
			Status:  coldp.SynonymTS,
		})
		if err != nil {
			return err
		}
		synonymID = sid
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hist, err := a.NomenclaturalHistory(ctx, taxonID)
	if err != nil {
		t.Fatalf("NomenclaturalHistory: %v", err)
	}
	if len(hist.Clusters) != 2 {
		t.Fatalf("expected 2 clusters (accepted + synonym family), got %d", len(hist.Clusters))
	}
	if hist.Clusters[0].Role != "accepted" {
		t.Errorf("first cluster role = %q, want 'accepted'", hist.Clusters[0].Role)
	}
	syn := hist.Clusters[1]
	if syn.Role != "synonym" {
		t.Errorf("second cluster role = %q, want 'synonym'", syn.Role)
	}
	// The synonym cluster contains the basionym + the recombination
	// (which is the synonym's name). The recombination's involvement
	// must carry the synonym-link id.
	var found bool
	for _, n := range syn.Names {
		if n.NameID == synName {
			found = true
			if n.Involvement != "synonym" {
				t.Errorf("synonym-name involvement = %q, want 'synonym'", n.Involvement)
			}
			if n.SynonymID != synonymID {
				t.Errorf("synonym-name SynonymID = %q, want %q", n.SynonymID, synonymID)
			}
		}
	}
	if !found {
		t.Fatalf("synonym cluster did not contain the synonym's name %q", synName)
	}
}

// TestNomenclaturalHistorySynonymSharingBasionymMergesIntoAccepted —
// when a synonym is really an obsolete recombination of the same
// basionym as the accepted name, both should land in the accepted
// cluster (not a separate synonym cluster). Curators reading a
// synonymy paragraph expect that grouping.
func TestNomenclaturalHistorySynonymSharingBasionymMergesIntoAccepted(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var (
		taxonID, basionymName, acceptedName, obsoleteName, synonymID string
	)
	err := a.WithTx(ctx, func(tx *Tx) error {
		basionymName = insertTestName(t, tx, "Felis leo")
		acceptedName = insertTestName(t, tx, "Panthera leo")
		obsoleteName = insertTestName(t, tx, "Leo leo")
		id, err := tx.CreateTaxon(taxonForTest(acceptedName))
		if err != nil {
			return err
		}
		taxonID = id
		if err := tx.LinkNameRelation(coldp.NameRelation{
			NameID:        acceptedName,
			RelatedNameID: basionymName,
			Type:          coldp.Basionym,
		}); err != nil {
			return err
		}
		if err := tx.LinkNameRelation(coldp.NameRelation{
			NameID:        obsoleteName,
			RelatedNameID: basionymName,
			Type:          coldp.Basionym,
		}); err != nil {
			return err
		}
		sid, err := tx.AddSynonym(coldp.Synonym{
			TaxonID: taxonID,
			NameID:  obsoleteName,
			Status:  coldp.SynonymTS,
		})
		if err != nil {
			return err
		}
		synonymID = sid
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hist, err := a.NomenclaturalHistory(ctx, taxonID)
	if err != nil {
		t.Fatalf("NomenclaturalHistory: %v", err)
	}
	if len(hist.Clusters) != 1 {
		t.Fatalf("expected 1 cluster (shared basionym merges), got %d", len(hist.Clusters))
	}
	c := hist.Clusters[0]
	if c.Role != "accepted" {
		t.Errorf("cluster role = %q, want 'accepted'", c.Role)
	}
	if len(c.Names) != 3 {
		t.Fatalf("expected 3 names (basionym + accepted + obsolete-synonym), got %d: %+v",
			len(c.Names), c.Names)
	}
	// Find each by NameID and check involvement.
	byID := map[string]NomenName{}
	for _, n := range c.Names {
		byID[n.NameID] = n
	}
	if byID[basionymName].Involvement != "unlinked" {
		t.Errorf("basionym involvement = %q, want 'unlinked'", byID[basionymName].Involvement)
	}
	if byID[acceptedName].Involvement != "accepted" {
		t.Errorf("accepted involvement = %q, want 'accepted'", byID[acceptedName].Involvement)
	}
	if byID[obsoleteName].Involvement != "synonym" {
		t.Errorf("obsolete involvement = %q, want 'synonym'", byID[obsoleteName].Involvement)
	}
	if byID[obsoleteName].SynonymID != synonymID {
		t.Errorf("obsolete SynonymID = %q, want %q", byID[obsoleteName].SynonymID, synonymID)
	}
}

// TestNomenclaturalHistoryNoBasionymSingleton — a taxon with just
// an accepted name and no name_relations produces one cluster
// containing only that name, flagged as its own basionym (since
// nothing else claims to be its basionym).
func TestNomenclaturalHistoryNoBasionymSingleton(t *testing.T) {
	a := newTestArchive(t)
	ctx := WithActor(context.Background(), "tester")

	var taxonID, nameID string
	err := a.WithTx(ctx, func(tx *Tx) error {
		nameID = insertTestName(t, tx, "Ursus arctos")
		id, err := tx.CreateTaxon(taxonForTest(nameID))
		if err != nil {
			return err
		}
		taxonID = id
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	hist, err := a.NomenclaturalHistory(ctx, taxonID)
	if err != nil {
		t.Fatalf("NomenclaturalHistory: %v", err)
	}
	if len(hist.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(hist.Clusters))
	}
	c := hist.Clusters[0]
	if len(c.Names) != 1 || c.Names[0].NameID != nameID {
		t.Fatalf("expected singleton cluster with just the accepted name; got %+v", c.Names)
	}
	if !c.Names[0].IsBasionym {
		t.Errorf("singleton cluster's only name should be flagged basionym")
	}
	if c.Names[0].Involvement != "accepted" {
		t.Errorf("singleton involvement = %q, want 'accepted'", c.Names[0].Involvement)
	}
}

// TestNomenclaturalHistoryTaxonNotFound — unknown taxon id returns
// ErrNotFound rather than an empty history.
func TestNomenclaturalHistoryTaxonNotFound(t *testing.T) {
	a := newTestArchive(t)
	ctx := context.Background()
	_, err := a.NomenclaturalHistory(ctx, "does-not-exist")
	if err == nil {
		t.Fatalf("expected ErrNotFound, got nil")
	}
}
