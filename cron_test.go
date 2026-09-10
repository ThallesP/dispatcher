package main

import (
	"path/filepath"
	"testing"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

func TestTemplateSnapshotPersistsCompleteMetrics(t *testing.T) {
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), "snapshots.duckdb")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TemplateSnapshot{}); err != nil {
		t.Fatal(err)
	}

	metrics := templateMetrics{
		TotalDeployments:        242,
		ActiveDeployments:       46,
		DeploymentsLast90Days:   11,
		TotalEarnings:           2411.65,
		EarningsLast90Days:      479.96,
		EarningsLast30Days:      172.78,
		TemplateHealth:          96,
		SupportHealth:           100,
		EligibleForSupportBonus: true,
	}
	snapshot := templateSnapshotAt(
		time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC),
		workspaceTemplate{ID: "template-1", Name: "Template", Code: "template", Status: "PUBLISHED", TotalPayout: 2411.65},
		&metrics,
	)
	if err := gorm.G[TemplateSnapshot](db).Create(t.Context(), &snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := gorm.G[TemplateSnapshot](db).Where("template_id = ?", "template-1").First(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if got.TotalDeployments == nil || *got.TotalDeployments != metrics.TotalDeployments ||
		got.ActiveDeployments == nil || *got.ActiveDeployments != metrics.ActiveDeployments ||
		got.DeploymentsLast90Days == nil || *got.DeploymentsLast90Days != metrics.DeploymentsLast90Days {
		t.Errorf("deployment metrics were not persisted: %+v", got)
	}
	if got.TotalEarnings == nil || *got.TotalEarnings != metrics.TotalEarnings ||
		got.EarningsLast90Days == nil || *got.EarningsLast90Days != metrics.EarningsLast90Days ||
		got.EarningsLast30Days == nil || *got.EarningsLast30Days != metrics.EarningsLast30Days {
		t.Errorf("earnings metrics were not persisted: %+v", got)
	}
	if got.TemplateHealth == nil || *got.TemplateHealth != metrics.TemplateHealth ||
		got.SupportHealth == nil || *got.SupportHealth != metrics.SupportHealth ||
		got.EligibleForSupportBonus == nil || *got.EligibleForSupportBonus != metrics.EligibleForSupportBonus {
		t.Errorf("health metrics were not persisted: %+v", got)
	}
	if got.Projects != metrics.TotalDeployments || got.ActiveProjects != metrics.ActiveDeployments ||
		got.RecentProjects != metrics.DeploymentsLast90Days || got.TotalPayout != 2411.65 {
		t.Errorf("legacy compatibility fields were not mirrored: %+v", got)
	}
}

func TestTemplateSnapshotLeavesUnpublishedMetricsUnknown(t *testing.T) {
	got := templateSnapshotAt(time.Now(), workspaceTemplate{ID: "draft", Status: "UNPUBLISHED"}, nil)
	if got.TotalDeployments != nil || got.TotalEarnings != nil || got.TemplateHealth != nil || got.EligibleForSupportBonus != nil {
		t.Fatalf("unpublished template received metrics: %+v", got)
	}
}
