package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// The raw endpoints hand out Dispatcher's collected rows as stored: every
// column, newest first, with no pivoting, ranking or folding. They exist so
// agents and scripts can answer questions the dashboard endpoints were not
// shaped for. Only collected data is exposed here, never credentials.

const (
	defaultRawLimit = 1000
	// maxRawLimit keeps a full response well under dispatcherctl's 16 MiB cap;
	// a snapshot row serializes to under 1 KiB.
	maxRawLimit = 10000
	// maxRawDays bounds ?days= at roughly ten years.
	maxRawDays = 3650
)

type rawQuery struct {
	Since *time.Time
	Until *time.Time
	Limit int
}

type rawResponse[T any] struct {
	Rows  []T `json:"rows"`
	Count int `json:"count"`
	// Truncated means more rows matched than limit allowed. The newest are
	// kept, so narrow the window or raise limit to reach older ones.
	Truncated bool `json:"truncated"`
}

// parseRawQuery reads the filters every raw endpoint shares: ?days=N or
// ?since= (mutually exclusive), ?until=, and ?limit=. Times take RFC 3339 or
// a plain YYYY-MM-DD date (UTC midnight).
func parseRawQuery(values url.Values, now time.Time) (rawQuery, error) {
	q := rawQuery{Limit: defaultRawLimit}
	if v := values.Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit < 1 || limit > maxRawLimit {
			return q, fmt.Errorf("limit must be between 1 and %d", maxRawLimit)
		}
		q.Limit = limit
	}
	if v := values.Get("days"); v != "" {
		if values.Get("since") != "" {
			return q, errors.New("pass days or since, not both")
		}
		days, err := strconv.Atoi(v)
		if err != nil || days < 1 || days > maxRawDays {
			return q, fmt.Errorf("days must be between 1 and %d", maxRawDays)
		}
		since := now.AddDate(0, 0, -days)
		q.Since = &since
	}
	for name, dst := range map[string]**time.Time{"since": &q.Since, "until": &q.Until} {
		v := values.Get(name)
		if v == "" {
			continue
		}
		t, err := parseRawTime(v)
		if err != nil {
			return q, fmt.Errorf("%s must be an RFC 3339 time or a YYYY-MM-DD date", name)
		}
		*dst = &t
	}
	if q.Since != nil && q.Until != nil && !q.Since.Before(*q.Until) {
		return q, errors.New("since must be before until")
	}
	return q, nil
}

func parseRawTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.DateOnly, v)
}

// apply scopes tx to the window on column and fetches one row past the limit,
// which is how newRawResponse tells a full page from a truncated one.
func (q rawQuery) apply(tx *gorm.DB, column string) *gorm.DB {
	if q.Since != nil {
		tx = tx.Where(column+" >= ?", *q.Since)
	}
	if q.Until != nil {
		tx = tx.Where(column+" < ?", *q.Until)
	}
	return tx.Order(column + " DESC").Limit(q.Limit + 1)
}

func newRawResponse[T any](rows []T, limit int) rawResponse[T] {
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	return rawResponse[T]{Rows: rows, Count: len(rows), Truncated: truncated}
}

// handleRawSnapshots serves template snapshots exactly as collected, legacy
// columns included. ?template= takes an id or code and scopes the rows to it.
func handleRawSnapshots(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := parseRawQuery(r.URL.Query(), time.Now().UTC())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		tx := db.WithContext(r.Context()).Model(&TemplateSnapshot{})
		if ref := r.URL.Query().Get("template"); ref != "" {
			template, ok, err := resolveTemplate(r.Context(), db, ref)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
				return
			}
			tx = tx.Where("template_id = ?", template.TemplateID)
		}
		rows := []TemplateSnapshot{}
		if err := q.apply(tx, "sampled_at").Order("id DESC").Find(&rows).Error; err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, newRawResponse(rows, q.Limit))
	}
}

// handleRawPayouts serves every mirrored payout, not just the handful the
// payout dashboard lists.
func handleRawPayouts(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := parseRawQuery(r.URL.Query(), time.Now().UTC())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rows := []Payout{}
		tx := db.WithContext(r.Context()).Model(&Payout{})
		if err := q.apply(tx, "created_at").Order("id DESC").Find(&rows).Error; err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, newRawResponse(rows, q.Limit))
	}
}
