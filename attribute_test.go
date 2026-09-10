package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

func TestPartitionPayoutsMatchesRealBatch(t *testing.T) {
	// Sep 8–10 2026: Twenty CRM moved +$2.67, Owncast +$2.04, seven payouts arrived.
	payouts := []pendingPayout{}
	for id, cents := range map[string]int64{"a": 204, "b": 125, "c": 13, "d": 118, "e": 5, "f": 3, "g": 3} {
		payouts = append(payouts, pendingPayout{ID: id, AmountCents: cents})
	}
	got := partitionPayouts(payouts, []templateDelta{{TemplateID: "twenty", Cents: 267}, {TemplateID: "owncast", Cents: 204}})
	if got == nil || got["a"] != "owncast" {
		t.Fatalf("expected the 204 payout on owncast, got %v", got)
	}
	for _, id := range []string{"b", "c", "d", "e", "f", "g"} {
		if got[id] != "twenty" {
			t.Fatalf("expected %s on twenty, got %v", id, got)
		}
	}
	if partitionPayouts([]pendingPayout{{ID: "x", AmountCents: 5}}, []templateDelta{{TemplateID: "t", Cents: 4}}) != nil {
		t.Fatal("expected no partition when amounts cannot fit")
	}
}

func TestAttributePayoutsUsesSnapshotDeltasAndWaitsWhenTheyDisagree(t *testing.T) {
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), "attr.duckdb")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TemplateSnapshot{}, &Payout{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t0 := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	f := func(v float64) *float64 { return &v }
	snap := func(at time.Time, id, name string, earned float64) TemplateSnapshot {
		return TemplateSnapshot{SampledAt: at, TemplateID: id, Name: name, Code: id, Status: "PUBLISHED", TotalPayout: earned, TotalEarnings: f(earned)}
	}
	if err := db.Create(&[]TemplateSnapshot{
		snap(t0, "twenty", "Twenty CRM", 50.00), snap(t0, "owncast", "Owncast", 0),
		snap(t1, "twenty", "Twenty CRM", 51.38), snap(t1, "owncast", "Owncast", 2.04),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A cash withdrawal is never attributed; a credit older than the first
	// snapshot is marked untracked rather than left looking pending.
	payouts := []Payout{
		{ID: "cash", CreatedAt: t0.Add(30 * time.Minute), AmountCents: 10000, Kind: "cash", Status: "COMPLETED"},
		{ID: "old", CreatedAt: t0.Add(-time.Hour), AmountCents: 7, Kind: "credits", Status: "COMPLETED"},
		{ID: "p125", CreatedAt: t0.Add(58 * time.Minute), AmountCents: 125, Kind: "credits", Status: "COMPLETED"},
		{ID: "p13", CreatedAt: t0.Add(58 * time.Minute), AmountCents: 13, Kind: "credits", Status: "COMPLETED"},
		{ID: "p204", CreatedAt: t0.Add(10 * time.Minute), AmountCents: 204, Kind: "credits", Status: "COMPLETED"},
	}
	if err := db.Create(&payouts).Error; err != nil {
		t.Fatal(err)
	}
	n, err := attributePayouts(ctx, db)
	if err != nil || n != 3 {
		t.Fatalf("want 3 attributed, got %d, %v", n, err)
	}
	got := map[string]string{}
	rows := []Payout{}
	if err := db.Raw(`SELECT id, template_id, template_name FROM payouts`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		got[r.ID] = r.TemplateID
	}
	want := map[string]string{"cash": "", "old": untrackedTemplateID, "p125": "twenty", "p13": "twenty", "p204": "owncast"}
	for id, tid := range want {
		if got[id] != tid {
			t.Fatalf("payout %s: want template %q, got %q (all: %v)", id, tid, got[id], got)
		}
	}
	// A payout the snapshots cannot explain yet stays pending.
	if err := db.Create(&Payout{ID: "late", CreatedAt: t1.Add(time.Minute), AmountCents: 99, Kind: "credits", Status: "COMPLETED"}).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := attributePayouts(ctx, db); err != nil || n != 0 {
		t.Fatalf("want 0 attributed while no newer snapshot exists, got %d, %v", n, err)
	}
	if err := db.Create(&[]TemplateSnapshot{snap(t1.Add(time.Hour), "twenty", "Twenty CRM", 51.38), snap(t1.Add(time.Hour), "owncast", "Owncast", 3.03)}).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := attributePayouts(ctx, db); err != nil || n != 1 {
		t.Fatalf("want the late payout attributed once a snapshot explains it, got %d, %v", n, err)
	}
	// A payout nothing explains is marked unknown once a snapshot lies beyond the
	// horizon, so payouts after it still get matched.
	t2 := t1.Add(2 * time.Hour)
	if err := db.Create(&[]Payout{
		{ID: "ghost", CreatedAt: t2.Add(time.Minute), AmountCents: 77, Kind: "credits", Status: "COMPLETED"},
		{ID: "p5", CreatedAt: t2.Add(30 * time.Minute), AmountCents: 5, Kind: "credits", Status: "COMPLETED"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]TemplateSnapshot{
		snap(t2.Add(time.Hour), "twenty", "Twenty CRM", 51.43), snap(t2.Add(time.Hour), "owncast", "Owncast", 3.03),
		snap(t2.Add(attributionHorizon+2*time.Hour), "twenty", "Twenty CRM", 51.43), snap(t2.Add(attributionHorizon+2*time.Hour), "owncast", "Owncast", 3.03),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := attributePayouts(ctx, db); err != nil || n != 1 {
		t.Fatalf("want p5 attributed after the ghost is given up, got %d, %v", n, err)
	}
	rows = nil
	if err := db.Raw(`SELECT id, template_id FROM payouts WHERE id IN ('ghost','p5')`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if (r.ID == "ghost" && r.TemplateID != unattributedTemplateID) || (r.ID == "p5" && r.TemplateID != "twenty") {
			t.Fatalf("unexpected attribution %s → %q", r.ID, r.TemplateID)
		}
	}
}
