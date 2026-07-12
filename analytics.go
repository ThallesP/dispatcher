package main

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// maxPayoutSeries caps how many templates get their own line in the payout
// chart; the rest are folded into a single "Other" series. Matches the five
// categorical chart colors on the frontend.
const maxPayoutSeries = 5

const otherSeriesKey = "other"

type payoutSeriesEntry struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type payoutSeriesPoint struct {
	SampledAt time.Time          `json:"sampledAt"`
	Values    map[string]float64 `json:"values"`
}

type payoutSeriesResponse struct {
	Series []payoutSeriesEntry `json:"series"`
	Points []payoutSeriesPoint `json:"points"`
}

type payoutSampleRow struct {
	SampledAt   time.Time
	TemplateID  string
	Name        string
	TotalPayout float64
}

// handlePayoutSeries returns per-template payout over time for a stacked
// chart: the top templates by payout as their own series plus an "Other"
// fold. ?days=N bounds the window (default 30).
func handlePayoutSeries(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := 30
		if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v >= 1 && v <= 365 {
			days = v
		}
		since := time.Now().UTC().AddDate(0, 0, -days)

		rows := []payoutSampleRow{}
		err := db.WithContext(r.Context()).Raw(`
			SELECT sampled_at, template_id, name, total_payout
			FROM template_snapshots
			WHERE sampled_at >= ?
			ORDER BY sampled_at`, since).Scan(&rows).Error
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, buildPayoutSeries(rows))
	}
}

// buildPayoutSeries pivots snapshot rows into chart series. Templates are
// ranked by their peak payout in the window (payouts are cumulative, so peak
// ≈ latest, but this also keeps templates that vanish mid-window ranked);
// ranking on the whole window keeps colors stable across 7d/30d/90d switches.
func buildPayoutSeries(rows []payoutSampleRow) payoutSeriesResponse {
	resp := payoutSeriesResponse{Series: []payoutSeriesEntry{}, Points: []payoutSeriesPoint{}}
	if len(rows) == 0 {
		return resp
	}

	peak := map[string]float64{}
	names := map[string]string{}
	ranked := []string{}
	for _, row := range rows {
		if _, seen := peak[row.TemplateID]; !seen {
			ranked = append(ranked, row.TemplateID)
			names[row.TemplateID] = row.Name
		}
		peak[row.TemplateID] = max(peak[row.TemplateID], row.TotalPayout)
	}
	sort.SliceStable(ranked, func(i, j int) bool { return peak[ranked[i]] > peak[ranked[j]] })

	// Biggest series first = bottom of the stack; everything past the cap
	// sums into "Other" so the chart never runs out of colors.
	topKeys := map[string]bool{}
	for i, id := range ranked {
		if i >= maxPayoutSeries {
			break
		}
		topKeys[id] = true
		resp.Series = append(resp.Series, payoutSeriesEntry{Key: id, Name: names[id]})
	}
	hasOther := len(ranked) > maxPayoutSeries
	if hasOther {
		resp.Series = append(resp.Series, payoutSeriesEntry{Key: otherSeriesKey, Name: "Other"})
	}

	for _, row := range rows {
		n := len(resp.Points)
		if n == 0 || !resp.Points[n-1].SampledAt.Equal(row.SampledAt) {
			// Zero-fill every series so stacking never sees a missing key.
			values := make(map[string]float64, len(resp.Series))
			for _, s := range resp.Series {
				values[s.Key] = 0
			}
			resp.Points = append(resp.Points, payoutSeriesPoint{SampledAt: row.SampledAt, Values: values})
			n++
		}
		point := resp.Points[n-1]
		if topKeys[row.TemplateID] {
			point.Values[row.TemplateID] += row.TotalPayout
		} else {
			point.Values[otherSeriesKey] += row.TotalPayout
		}
	}
	return resp
}

type metricChange struct {
	Current   float64  `json:"current"`
	Previous  *float64 `json:"previous"`
	ChangePct *float64 `json:"changePct"`
}

type analyticsSummary struct {
	SampledAt      time.Time    `json:"sampledAt"`
	ComparedTo     *time.Time   `json:"comparedTo"`
	TotalPayout    metricChange `json:"totalPayout"`
	Projects       metricChange `json:"projects"`
	RecentProjects metricChange `json:"recentProjects"`
	ActiveProjects metricChange `json:"activeProjects"`
}

type snapshotTotals struct {
	TotalPayout    float64
	Projects       float64
	RecentProjects float64
	ActiveProjects float64
}

// handleAnalyticsSummary returns workspace totals from the latest sample next
// to the sample closest to a week earlier, as "+10% vs last week" material.
// Responds null until the first snapshot lands.
func handleAnalyticsSummary(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		latest, previous, err := comparisonTimestamps(r.Context(), db)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if latest == nil {
			writeJSON(w, http.StatusOK, nil)
			return
		}

		current, err := totalsAt(r.Context(), db, *latest)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		var prev *snapshotTotals
		if previous != nil {
			prev, err = totalsAtPtr(r.Context(), db, *previous)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}

		writeJSON(w, http.StatusOK, analyticsSummary{
			SampledAt:      *latest,
			ComparedTo:     previous,
			TotalPayout:    changeOf(current.TotalPayout, prev, func(t snapshotTotals) float64 { return t.TotalPayout }),
			Projects:       changeOf(current.Projects, prev, func(t snapshotTotals) float64 { return t.Projects }),
			RecentProjects: changeOf(current.RecentProjects, prev, func(t snapshotTotals) float64 { return t.RecentProjects }),
			ActiveProjects: changeOf(current.ActiveProjects, prev, func(t snapshotTotals) float64 { return t.ActiveProjects }),
		})
	}
}

type templateAnalytics struct {
	TemplateID      string   `json:"templateId"`
	Name            string   `json:"name"`
	Code            string   `json:"code"`
	Status          string   `json:"status"`
	Health          *float64 `json:"health"`
	Projects        int64    `json:"projects"`
	RecentProjects  int64    `json:"recentProjects"`
	ActiveProjects  int64    `json:"activeProjects"`
	TotalPayout     float64  `json:"totalPayout"`
	PayoutPrevious  *float64 `json:"payoutPrevious"`
	PayoutChangePct *float64 `json:"payoutChangePct"`
}

type templateAnalyticsResponse struct {
	SampledAt  time.Time           `json:"sampledAt"`
	ComparedTo *time.Time          `json:"comparedTo"`
	Templates  []templateAnalytics `json:"templates"`
}

// handleTemplateAnalytics returns the latest snapshot of every template with
// its payout change versus ~a week ago.
func handleTemplateAnalytics(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		latest, previous, err := comparisonTimestamps(r.Context(), db)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if latest == nil {
			writeJSON(w, http.StatusOK, nil)
			return
		}

		// LEFT JOIN against a zero timestamp matches nothing, keeping the
		// query shape identical when there is no comparison sample yet.
		joinAt := time.Time{}
		if previous != nil {
			joinAt = *previous
		}
		templates := []templateAnalytics{}
		err = db.WithContext(r.Context()).Raw(`
			SELECT cur.template_id, cur.name, cur.code, cur.status, cur.health,
			       cur.projects, cur.recent_projects, cur.active_projects, cur.total_payout,
			       prev.total_payout AS payout_previous
			FROM template_snapshots cur
			LEFT JOIN template_snapshots prev
			  ON prev.template_id = cur.template_id AND prev.sampled_at = ?
			WHERE cur.sampled_at = ?
			ORDER BY cur.total_payout DESC, cur.name`, joinAt, *latest).Scan(&templates).Error
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		for i := range templates {
			t := &templates[i]
			t.PayoutChangePct = pctChange(t.TotalPayout, t.PayoutPrevious)
		}
		writeJSON(w, http.StatusOK, templateAnalyticsResponse{
			SampledAt:  *latest,
			ComparedTo: previous,
			Templates:  templates,
		})
	}
}

// comparisonTimestamps picks the latest sample and the sample to compare it
// against: the newest one at least 7 days older, falling back to the oldest
// available so young deployments still get a delta. latest is nil while the
// snapshots table is empty; previous is nil when latest is the only sample.
func comparisonTimestamps(ctx context.Context, db *gorm.DB) (latest, previous *time.Time, err error) {
	latest, err = scanTime(ctx, db, "SELECT max(sampled_at) FROM template_snapshots")
	if err != nil || latest == nil {
		return nil, nil, err
	}
	previous, err = scanTime(ctx, db,
		"SELECT max(sampled_at) FROM template_snapshots WHERE sampled_at <= ? - INTERVAL 7 DAY", *latest)
	if err != nil {
		return nil, nil, err
	}
	if previous == nil {
		previous, err = scanTime(ctx, db,
			"SELECT min(sampled_at) FROM template_snapshots WHERE sampled_at < ?", *latest)
		if err != nil {
			return nil, nil, err
		}
	}
	return latest, previous, nil
}

func scanTime(ctx context.Context, db *gorm.DB, query string, args ...any) (*time.Time, error) {
	var t sql.NullTime
	if err := db.WithContext(ctx).Raw(query, args...).Row().Scan(&t); err != nil {
		return nil, err
	}
	if !t.Valid {
		return nil, nil
	}
	utc := t.Time.UTC()
	return &utc, nil
}

func totalsAt(ctx context.Context, db *gorm.DB, at time.Time) (snapshotTotals, error) {
	var totals snapshotTotals
	err := db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(total_payout), 0) AS total_payout,
		       COALESCE(SUM(projects), 0) AS projects,
		       COALESCE(SUM(recent_projects), 0) AS recent_projects,
		       COALESCE(SUM(active_projects), 0) AS active_projects
		FROM template_snapshots
		WHERE sampled_at = ?`, at).Scan(&totals).Error
	return totals, err
}

func totalsAtPtr(ctx context.Context, db *gorm.DB, at time.Time) (*snapshotTotals, error) {
	totals, err := totalsAt(ctx, db, at)
	if err != nil {
		return nil, err
	}
	return &totals, nil
}

func changeOf(current float64, prev *snapshotTotals, pick func(snapshotTotals) float64) metricChange {
	if prev == nil {
		return metricChange{Current: current}
	}
	value := pick(*prev)
	return metricChange{Current: current, Previous: &value, ChangePct: pctChange(current, &value)}
}

func pctChange(current float64, previous *float64) *float64 {
	if previous == nil || *previous == 0 {
		return nil
	}
	pct := (current - *previous) / *previous * 100
	return &pct
}

// handleRefreshAnalytics collects a fresh template snapshot on demand, so a
// first-time user doesn't sit in an empty dashboard waiting for the hourly
// cron.
func handleRefreshAnalytics(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := collectTemplateSnapshots(db); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
