package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

func TestBuildPayoutSeriesFoldsAndZeroFills(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	// 7 templates: 5 should keep their own series, t6+t7 fold into "other".
	// t7 exists only in the first sample (deleted template) — later points
	// must still carry a zero for "other" rather than a missing key.
	// Rows arrive ordered by sampled_at, as the SQL guarantees.
	payouts := []float64{700, 600, 500, 400, 300, 200, 100}
	rows := []payoutSampleRow{}
	for i, payout := range payouts {
		id := string(rune('a' + i))
		rows = append(rows, payoutSampleRow{SampledAt: t0, TemplateID: id, Name: "tpl-" + id, TotalPayout: payout})
	}
	for i, payout := range payouts[:6] {
		id := string(rune('a' + i))
		rows = append(rows, payoutSampleRow{SampledAt: t1, TemplateID: id, Name: "tpl-" + id, TotalPayout: payout + 10})
	}

	got := buildPayoutSeries(rows)

	wantSeries := []payoutSeriesEntry{
		{Key: "a", Name: "tpl-a"}, {Key: "b", Name: "tpl-b"}, {Key: "c", Name: "tpl-c"},
		{Key: "d", Name: "tpl-d"}, {Key: "e", Name: "tpl-e"}, {Key: "other", Name: "Other"},
	}
	if len(got.Series) != len(wantSeries) {
		t.Fatalf("series count = %d, want %d (%v)", len(got.Series), len(wantSeries), got.Series)
	}
	for i, want := range wantSeries {
		if got.Series[i] != want {
			t.Errorf("series[%d] = %v, want %v", i, got.Series[i], want)
		}
	}

	if len(got.Points) != 2 {
		t.Fatalf("points count = %d, want 2", len(got.Points))
	}
	p0, p1 := got.Points[0], got.Points[1]
	if p0.Values["other"] != 300 { // 200 + 100
		t.Errorf("point0 other = %v, want 300", p0.Values["other"])
	}
	if p1.Values["other"] != 210 { // only t6 remains: 200+10
		t.Errorf("point1 other = %v, want 210", p1.Values["other"])
	}
	if p1.Values["a"] != 710 {
		t.Errorf("point1 a = %v, want 710", p1.Values["a"])
	}
	for _, s := range got.Series {
		if _, ok := p1.Values[s.Key]; !ok {
			t.Errorf("point1 missing key %q — stacking needs zero-fill", s.Key)
		}
	}
}

func TestBuildPayoutSeriesNoFoldUnderCap(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows := []payoutSampleRow{
		{SampledAt: t0, TemplateID: "x", Name: "X", TotalPayout: 5},
		{SampledAt: t0, TemplateID: "y", Name: "Y", TotalPayout: 9},
	}
	got := buildPayoutSeries(rows)
	if len(got.Series) != 2 || got.Series[0].Key != "y" || got.Series[1].Key != "x" {
		t.Fatalf("series = %v, want [y x] ranked by payout with no other fold", got.Series)
	}
	if _, ok := got.Points[0].Values["other"]; ok {
		t.Errorf("unexpected other key when under the cap")
	}
}

func TestBuildPayoutSeriesEmpty(t *testing.T) {
	got := buildPayoutSeries(nil)
	if got.Series == nil || got.Points == nil {
		t.Fatalf("empty input must serialize as [] not null: %+v", got)
	}
	if len(got.Series) != 0 || len(got.Points) != 0 {
		t.Fatalf("want empty response, got %+v", got)
	}
}

func TestAnalyticsTotalsUseAuthoritativeTemplateMetrics(t *testing.T) {
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), "analytics.duckdb")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TemplateSnapshot{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	rows := []TemplateSnapshot{
		{
			SampledAt: at, TemplateID: "current", TotalDeployments: pointerTo(int64(20)),
			ActiveDeployments: pointerTo(int64(8)), DeploymentsLast90Days: pointerTo(int64(5)),
			TotalEarnings: pointerTo(125.50),
		},
		// A legacy row can contain values from the old, incorrect resolver but
		// has no authoritative metrics and must not affect current totals.
		{SampledAt: at, TemplateID: "legacy", Projects: 999, ActiveProjects: 999, RecentProjects: 999, TotalPayout: 999},
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(t.Context(), &rows, 100); err != nil {
		t.Fatal(err)
	}

	got, err := totalsAt(t.Context(), db, at)
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects != 20 || got.ActiveProjects != 8 || got.RecentProjects != 5 || got.TotalPayout != 125.50 {
		t.Fatalf("totals = %+v", got)
	}
}

func TestTemplateProjectsChartsOneTemplateFromAuthoritativeMetrics(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour)
	day := 24 * time.Hour
	sample := func(id, code string, at time.Time, total, recent, active int64) TemplateSnapshot {
		return TemplateSnapshot{
			SampledAt: at, TemplateID: id, Name: "Template " + id, Code: code, Status: "PUBLISHED",
			TotalDeployments: pointerTo(total), DeploymentsLast90Days: pointerTo(recent),
			ActiveDeployments: pointerTo(active), TotalEarnings: pointerTo(1.0),
		}
	}
	rows := []TemplateSnapshot{
		sample("tpl-a", "old-code", now.Add(-40*day), 1, 1, 1), // outside the 30d window
		sample("tpl-a", "old-code", now.Add(-20*day), 100, 40, 20),
		// Legacy rows carry values from the old resolver and must not chart.
		{SampledAt: now.Add(-10 * day), TemplateID: "tpl-a", Code: "old-code", Projects: 999, ActiveProjects: 999},
		sample("tpl-a", "new-code", now.Add(-1*day), 150, 30, 25),
		sample("tpl-b", "other", now.Add(-1*day), 7, 7, 7),
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(t.Context(), &rows, 100); err != nil {
		t.Fatal(err)
	}

	// A template still answers to the code it was renamed from, and is
	// described by its latest snapshot.
	res := serveTemplateProjects(t, db, "old-code", "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	var got templateProjectsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TemplateID != "tpl-a" || got.Code != "new-code" || got.Days != 30 {
		t.Fatalf("template = %+v, days = %d", got.templateRef, got.Days)
	}
	if len(got.Points) != 2 || got.Points[0].Projects != 100 || got.Points[1].Projects != 150 {
		t.Fatalf("points = %+v", got.Points)
	}
	c := got.Change
	if c == nil || c.Projects.Current != 150 || *c.Projects.Previous != 100 || *c.Projects.ChangePct != 50 {
		t.Fatalf("projects change = %+v", c)
	}
	if c.RecentProjects.Current != 30 || *c.RecentProjects.ChangePct != -25 {
		t.Fatalf("recent change = %+v", c.RecentProjects)
	}

	if res := serveTemplateProjects(t, db, "tpl-a", "days=90"); !strings.Contains(res.Body.String(), `"projects":1,`) {
		t.Fatalf("90d window should include the 40-day-old sample: %s", res.Body)
	}
	if res := serveTemplateProjects(t, db, "missing", ""); res.Code != http.StatusNotFound {
		t.Fatalf("unknown template status = %d", res.Code)
	}
}

func TestBuildProjectChangesWithoutBaseline(t *testing.T) {
	if got := buildProjectChanges(nil); got != nil {
		t.Fatalf("no samples should mean no change, got %+v", got)
	}
	got := buildProjectChanges([]projectPoint{{Projects: 5, RecentProjects: 3, ActiveProjects: 1}})
	if got.Projects.Current != 5 || got.Projects.Previous != nil || got.Projects.ChangePct != nil {
		t.Fatalf("a single sample has no baseline: %+v", got.Projects)
	}
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(duckdb.Open(filepath.Join(t.TempDir(), "test.duckdb")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TemplateSnapshot{}, &Payout{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func serveTemplateProjects(t *testing.T, db *gorm.DB, template, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/templates/"+template+"/projects?"+query, nil)
	req.SetPathValue("template", template)
	res := httptest.NewRecorder()
	handleTemplateProjects(db)(res, req)
	return res
}
