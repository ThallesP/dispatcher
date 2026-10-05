package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestParseRawQuery(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

	q, err := parseRawQuery(url.Values{}, now)
	if err != nil || q.Limit != defaultRawLimit || q.Since != nil || q.Until != nil {
		t.Fatalf("defaults = %+v, %v", q, err)
	}

	q, err = parseRawQuery(url.Values{"days": {"7"}, "until": {"2026-10-04"}, "limit": {"50"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Since.Equal(now.AddDate(0, 0, -7)) || !q.Until.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) || q.Limit != 50 {
		t.Fatalf("query = %+v", q)
	}

	q, err = parseRawQuery(url.Values{"since": {"2026-09-01T10:00:00-03:00"}}, now)
	if err != nil || !q.Since.Equal(time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339 since = %+v, %v", q.Since, err)
	}

	for _, bad := range []url.Values{
		{"days": {"7"}, "since": {"2026-09-01"}},
		{"days": {"0"}},
		{"limit": {"0"}},
		{"limit": {"10001"}},
		{"since": {"yesterday"}},
		{"since": {"2026-10-02"}, "until": {"2026-10-01"}},
	} {
		if _, err := parseRawQuery(bad, now); err == nil {
			t.Errorf("parseRawQuery(%v) accepted invalid filters", bad)
		}
	}
}

func TestRawSnapshotsFiltersNewestFirstAndFlagsTruncation(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	rows := []TemplateSnapshot{}
	for i := range 5 {
		at := base.Add(time.Duration(i) * time.Hour)
		rows = append(rows,
			TemplateSnapshot{SampledAt: at, TemplateID: "tpl-a", Code: "alpha", TemplateHealth: pointerTo(float64(90 + i))},
			TemplateSnapshot{SampledAt: at, TemplateID: "tpl-b", Code: "beta"},
		)
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(t.Context(), &rows, 100); err != nil {
		t.Fatal(err)
	}

	got := serveRaw[TemplateSnapshot](t, handleRawSnapshots(db), "template=alpha&limit=3")
	if got.Count != 3 || !got.Truncated {
		t.Fatalf("count = %d, truncated = %t", got.Count, got.Truncated)
	}
	for i, row := range got.Rows {
		if row.TemplateID != "tpl-a" || *row.TemplateHealth != float64(94-i) {
			t.Fatalf("row %d = %+v, want tpl-a newest first", i, row)
		}
	}

	got = serveRaw[TemplateSnapshot](t, handleRawSnapshots(db), "since=2026-09-01T03:00:00Z")
	if got.Count != 4 || got.Truncated {
		t.Fatalf("since filter: count = %d, truncated = %t", got.Count, got.Truncated)
	}

	res := httptest.NewRecorder()
	handleRawSnapshots(db)(res, httptest.NewRequest(http.MethodGet, "/api/raw/snapshots?template=nope", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown template status = %d", res.Code)
	}
	res = httptest.NewRecorder()
	handleRawSnapshots(db)(res, httptest.NewRequest(http.MethodGet, "/api/raw/snapshots?limit=abc", nil))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "limit") {
		t.Fatalf("bad limit: status = %d, body = %s", res.Code, res.Body)
	}
}

func TestRawPayoutsReturnsEveryStoredPayout(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	payouts := []Payout{}
	for i := range recentPayoutRows + 4 {
		payouts = append(payouts, Payout{
			ID: "wd-" + string(rune('a'+i)), CreatedAt: base.AddDate(0, 0, i*7),
			AmountCents: int64(10000 + i), Status: "COMPLETED", Kind: "cash",
		})
	}
	if err := gorm.G[Payout](db).CreateInBatches(t.Context(), &payouts, 100); err != nil {
		t.Fatal(err)
	}

	got := serveRaw[Payout](t, handleRawPayouts(db), "")
	if got.Count != len(payouts) || got.Truncated {
		t.Fatalf("count = %d (want %d), truncated = %t", got.Count, len(payouts), got.Truncated)
	}
	if got.Rows[0].ID != payouts[len(payouts)-1].ID {
		t.Fatalf("first row = %s, want the newest payout", got.Rows[0].ID)
	}
}

func serveRaw[T any](t *testing.T, handler http.HandlerFunc, query string) rawResponse[T] {
	t.Helper()
	res := httptest.NewRecorder()
	handler(res, httptest.NewRequest(http.MethodGet, "/api/raw?"+query, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	var got rawResponse[T]
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}
