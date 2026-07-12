package main

import (
	"testing"
	"time"
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
