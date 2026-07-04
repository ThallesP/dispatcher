package main

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"
)

type payoutPoint struct {
	SampledAt   time.Time `json:"sampledAt"`
	TotalPayout float64   `json:"totalPayout"`
}

// handlePayoutSeries returns the workspace-wide total payout per sample so it
// can be charted over time. ?days=N bounds the window (default 30).
func handlePayoutSeries(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := 30
		if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v >= 1 && v <= 365 {
			days = v
		}
		since := time.Now().UTC().AddDate(0, 0, -days)

		points := []payoutPoint{}
		err := db.WithContext(r.Context()).Raw(`
			SELECT sampled_at, SUM(total_payout) AS total_payout
			FROM template_snapshots
			WHERE sampled_at >= ?
			GROUP BY sampled_at
			ORDER BY sampled_at`, since).Scan(&points).Error
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, points)
	}
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
