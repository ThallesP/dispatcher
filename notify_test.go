package main

import (
	"path/filepath"
	"testing"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

func testNotificationTarget(t *testing.T, body string, headers map[string]string) NotificationTarget {
	t.Helper()
	headersJSON, err := encodeNotificationHeaders(headers)
	if err != nil {
		t.Fatal(err)
	}
	return NotificationTarget{
		Name:            "Test target",
		Kind:            "custom",
		URL:             "https://example.com/webhook",
		HeadersJSON:     headersJSON,
		BodyTemplate:    body,
		OnPayout:        true,
		OnHealthDrop:    true,
		OnWeeklySummary: true,
	}
}

func TestValidateNotificationPreset(t *testing.T) {
	for kind, preset := range notificationPresets {
		if kind == "custom" {
			continue
		}
		target := testNotificationTarget(t, preset.BodyTemplate, preset.Headers)
		target.Kind = kind
		if err := validateNotificationTarget(target); err != nil {
			t.Errorf("%s preset did not validate: %v", kind, err)
		}
	}
}

func TestValidateNotificationRejectsMissingKey(t *testing.T) {
	target := testNotificationTarget(t, `{"content": {{json .Missing}}}`, map[string]string{"Content-Type": "application/json"})
	if err := validateNotificationTarget(target); err == nil {
		t.Fatal("expected a missing template key to be rejected")
	}
}

func TestValidateNotificationRejectsInvalidJSON(t *testing.T) {
	target := testNotificationTarget(t, `{"content": {{.Message}}}`, map[string]string{"Content-Type": "application/json"})
	if err := validateNotificationTarget(target); err == nil {
		t.Fatal("expected invalid rendered JSON to be rejected")
	}
}

func TestValidateNotificationRejectsHeaderNewline(t *testing.T) {
	target := testNotificationTarget(t, `{"ok": true}`, map[string]string{"X-Test": "hello\r\nInjected: yes"})
	if err := validateNotificationTarget(target); err == nil {
		t.Fatal("expected CR/LF in a header value to be rejected")
	}
}

func TestCrossedHealthThreshold(t *testing.T) {
	value := func(v float64) *float64 { return &v }
	tests := []struct {
		name     string
		previous *float64
		current  *float64
		want     bool
	}{
		{name: "crosses", previous: value(95), current: value(89), want: true},
		{name: "starts at threshold", previous: value(90), current: value(80), want: true},
		{name: "stays healthy", previous: value(95), current: value(91)},
		{name: "already unhealthy", previous: value(89), current: value(70)},
		{name: "recovers", previous: value(80), current: value(95)},
		{name: "previous unknown", current: value(80)},
		{name: "current unknown", previous: value(95)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crossedHealthThreshold(tt.previous, tt.current); got != tt.want {
				t.Fatalf("crossedHealthThreshold() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAggregateWeeklySnapshots(t *testing.T) {
	from := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	healthA := 96.0
	healthB := 88.0
	snapshots := []TemplateSnapshot{
		{ID: 1, SampledAt: from.Add(-48 * time.Hour), TemplateID: "a", Name: "Alpha", Projects: 5, TotalPayout: 10},
		{ID: 2, SampledAt: from.Add(-time.Hour), TemplateID: "a", Name: "Alpha", Projects: 7, TotalPayout: 14},
		{ID: 3, SampledAt: from.Add(72 * time.Hour), TemplateID: "a", Name: "Alpha", Projects: 10, TotalPayout: 19},
		{ID: 4, SampledAt: to, TemplateID: "a", Name: "Alpha", Health: &healthA, Projects: 12, TotalPayout: 22.5},
		// Beta has no pre-window observation, so its first in-window sample is
		// the baseline.
		{ID: 5, SampledAt: from.Add(time.Hour), TemplateID: "b", Name: "Beta", Projects: 2, TotalPayout: 3},
		{ID: 6, SampledAt: to.Add(-time.Hour), TemplateID: "b", Name: "Beta", Health: &healthB, Projects: 1, TotalPayout: 5.5},
		{ID: 7, SampledAt: to.Add(time.Hour), TemplateID: "a", Name: "Alpha", Projects: 999, TotalPayout: 999},
	}

	got, ok := aggregateWeeklySnapshots(snapshots, from, to)
	if !ok {
		t.Fatal("expected a weekly summary")
	}
	if len(got.Templates) != 2 {
		t.Fatalf("templates = %d, want 2", len(got.Templates))
	}
	alpha, beta := got.Templates[0], got.Templates[1]
	if alpha.Name != "Alpha" || alpha.NetNewProjects != 5 || alpha.PayoutDelta != 8.5 {
		t.Errorf("alpha = %+v, want +5 projects and +8.5 payout", alpha)
	}
	if alpha.Health == nil || *alpha.Health != 96 {
		t.Errorf("alpha health = %v, want 96", alpha.Health)
	}
	if beta.Name != "Beta" || beta.NetNewProjects != -1 || beta.PayoutDelta != 2.5 {
		t.Errorf("beta = %+v, want -1 project and +2.5 payout", beta)
	}
	if got.TotalNetNewProjects != 4 || got.TotalPayoutDelta != 11 {
		t.Errorf("totals = %+v, want +4 projects and +11 payout", got)
	}
}

func TestAggregateWeeklySnapshotsEmpty(t *testing.T) {
	to := time.Now().UTC()
	if _, ok := aggregateWeeklySnapshots(nil, to.AddDate(0, 0, -7), to); ok {
		t.Fatal("empty snapshots should not produce a summary")
	}
}

func TestLoadWeeklySummary(t *testing.T) {
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), "notify.duckdb")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TemplateSnapshot{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	rows := []TemplateSnapshot{
		{SampledAt: now.AddDate(0, 0, -8), TemplateID: "a", Name: "Alpha", Projects: 2, TotalPayout: 4},
		{SampledAt: now.AddDate(0, 0, -7).Add(-time.Hour), TemplateID: "a", Name: "Alpha", Projects: 3, TotalPayout: 5},
		{SampledAt: now.Add(-time.Hour), TemplateID: "a", Name: "Alpha", Projects: 8, TotalPayout: 12},
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(t.Context(), &rows, 100); err != nil {
		t.Fatal(err)
	}

	ev, ok, err := loadWeeklySummary(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a weekly event")
	}
	data, ok := ev.Data.(WeeklySummaryNotificationData)
	if !ok {
		t.Fatalf("event data has type %T", ev.Data)
	}
	if len(data.Templates) != 1 || data.TotalNetNewProjects != 5 || data.TotalPayoutDelta != 7 {
		t.Fatalf("weekly data = %+v, want +5 projects and +7 payout", data)
	}
}
