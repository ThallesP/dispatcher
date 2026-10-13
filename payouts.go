package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// railwayCreditWithdrawalTypename is the union arm Railway uses for payouts
// taken as Railway credits instead of cash.
const railwayCreditWithdrawalTypename = "CreditWithdrawalInfo"

const (
	// dayKeyLayout is both the daily bucket key and the wire format for a
	// point on the chart.
	dayKeyLayout = "2006-01-02"
	// recentPayoutRows caps the payout table. The chart carries the shape of
	// the history, so the table only has to show what happened lately.
	recentPayoutRows = 8
	// defaultPayoutWindowDays matches the analytics chart's default range.
	defaultPayoutWindowDays = 30
	maxPayoutWindowDays     = 365
)

// voidPayoutStatuses are the withdrawal states where no money actually moved.
// Everything else — COMPLETED, PENDING, and any state Railway adds later —
// counts toward the totals, so an unrecognized status never silently drops a
// payout out of the chart.
var voidPayoutStatuses = map[string]bool{
	"FAILED":    true,
	"CANCELLED": true,
	"CANCELED":  true,
	"REJECTED":  true,
}

func isVoidPayout(status string) bool { return voidPayoutStatuses[status] }

// payoutPoint is one day of the chart. Amounts are cumulative from the start
// of the selected window, so the line only ever climbs; Count is the running
// number of payouts behind it.
type payoutPoint struct {
	Date         string `json:"date"` // YYYY-MM-DD
	CashCents    int64  `json:"cashCents"`
	CreditsCents int64  `json:"creditsCents"`
	Count        int    `json:"count"`
}

// payoutWindow summarizes the selected range against the range of equal length
// immediately before it, for a "+8.4% vs previous 30d" read.
type payoutWindow struct {
	Days          int      `json:"days"`
	TotalCents    int64    `json:"totalCents"`
	PreviousCents int64    `json:"previousCents"`
	ChangePct     *float64 `json:"changePct"`
	Count         int      `json:"count"`
}

// payoutTotals carries the lifetime figures the chart deliberately does not
// plot. The chart is scoped to the selected window; a cumulative line that
// also spanned all time would need a second y-scale, whose alignment is
// arbitrary and invents a trend.
type payoutTotals struct {
	LifetimeCents int64      `json:"lifetimeCents"`
	CashCents     int64      `json:"cashCents"`
	CreditsCents  int64      `json:"creditsCents"`
	PendingCents  int64      `json:"pendingCents"`
	PendingCount  int        `json:"pendingCount"`
	Count         int        `json:"count"`
	FirstPayoutAt *time.Time `json:"firstPayoutAt"`
	LastPayoutAt  *time.Time `json:"lastPayoutAt"`
}

// payoutTemplateTotal is one line of the per-template breakdown: how many
// credit payouts a template earned in the selected window and what they added
// up to. Payouts still awaiting attribution group under "pending", ones the
// matcher gave up on under "unknown" (see attribute.go).
type payoutTemplateTotal struct {
	TemplateID   string `json:"templateId"`
	TemplateName string `json:"templateName"`
	Count        int    `json:"count"`
	Cents        int64  `json:"cents"`
}

type payoutHistoryResponse struct {
	Points []payoutPoint `json:"points"`
	Window payoutWindow  `json:"window"`
	Totals payoutTotals  `json:"totals"`
	// Payouts is the most recent recentPayoutRows rows, newest first, and
	// TotalRows how many exist in all — so the table can say what it is hiding.
	Payouts   []Payout `json:"payouts"`
	TotalRows int      `json:"totalRows"`
	// ByTemplate breaks the window's credit payouts down per template, largest
	// earner first.
	ByTemplate []payoutTemplateTotal `json:"byTemplate"`
}

// handlePayoutHistory serves the payout dashboard straight from DuckDB.
// Railway is still the source of truth, but its history is a paginated
// connection with no aggregate resolver, so it is mirrored by the collector
// (see syncPayouts) and read locally. ?days=N bounds the chart window.
func handlePayoutHistory(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := defaultPayoutWindowDays
		if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v >= 1 && v <= maxPayoutWindowDays {
			days = v
		}
		payouts := []Payout{}
		err := db.WithContext(r.Context()).Raw(`
			SELECT id, created_at, amount_cents, status, kind, destination, template_id, template_name
			FROM payouts
			ORDER BY created_at DESC`).Scan(&payouts).Error
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, buildPayoutHistory(payouts, days, time.Now().UTC()))
	}
}

// buildPayoutHistory turns the stored payouts into the windowed cumulative
// series, the window/previous-window comparison, lifetime totals, the recent
// rows the table shows and the per-template breakdown of the window.
func buildPayoutHistory(payouts []Payout, days int, now time.Time) payoutHistoryResponse {
	resp := payoutHistoryResponse{
		Points:     []payoutPoint{},
		Payouts:    []Payout{},
		ByTemplate: []payoutTemplateTotal{},
		Window:     payoutWindow{Days: days},
		TotalRows:  len(payouts),
	}

	// Newest first for the table. The SQL already orders this way, but the
	// pure function must not depend on its caller for correctness.
	sorted := slices.Clone(payouts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	for i, p := range sorted {
		if i >= recentPayoutRows {
			break
		}
		p.CreatedAt = p.CreatedAt.UTC()
		resp.Payouts = append(resp.Payouts, p)
	}

	// The window is the last N whole UTC days including today, so the axis
	// lines up with the daily buckets rather than a ragged clock time.
	today := now.UTC().Truncate(24 * time.Hour)
	windowStart := today.AddDate(0, 0, -(days - 1))
	previousStart := windowStart.AddDate(0, 0, -days)

	perDayCash := map[string]int64{}
	perDayCredits := map[string]int64{}
	perDayCount := map[string]int{}
	byTemplate := map[string]*payoutTemplateTotal{}

	for _, p := range sorted {
		at := p.CreatedAt.UTC()
		if isVoidPayout(p.Status) {
			continue
		}
		credits := p.Kind == "credits"

		resp.Totals.Count++
		resp.Totals.LifetimeCents += p.AmountCents
		if credits {
			resp.Totals.CreditsCents += p.AmountCents
		} else {
			resp.Totals.CashCents += p.AmountCents
		}
		if p.Status == "PENDING" {
			resp.Totals.PendingCount++
			resp.Totals.PendingCents += p.AmountCents
		}
		if resp.Totals.LastPayoutAt == nil || at.After(*resp.Totals.LastPayoutAt) {
			resp.Totals.LastPayoutAt = &at
		}
		if resp.Totals.FirstPayoutAt == nil || at.Before(*resp.Totals.FirstPayoutAt) {
			resp.Totals.FirstPayoutAt = &at
		}

		switch {
		case !at.Before(windowStart):
			key := at.Format(dayKeyLayout)
			perDayCount[key]++
			if credits {
				perDayCredits[key] += p.AmountCents
			} else {
				perDayCash[key] += p.AmountCents
			}
			resp.Window.TotalCents += p.AmountCents
			resp.Window.Count++
			if credits {
				key, name := p.TemplateID, p.TemplateName
				if key == "" {
					key, name = "pending", "pending"
				} else if name == "" {
					name = key
				}
				if byTemplate[key] == nil {
					byTemplate[key] = &payoutTemplateTotal{TemplateID: key, TemplateName: name}
				}
				byTemplate[key].Count++
				byTemplate[key].Cents += p.AmountCents
			}
		case !at.Before(previousStart):
			resp.Window.PreviousCents += p.AmountCents
		}
	}

	if resp.Window.PreviousCents > 0 {
		pct := float64(resp.Window.TotalCents-resp.Window.PreviousCents) / float64(resp.Window.PreviousCents) * 100
		resp.Window.ChangePct = &pct
	}
	for _, t := range byTemplate {
		resp.ByTemplate = append(resp.ByTemplate, *t)
	}
	sort.Slice(resp.ByTemplate, func(i, j int) bool {
		if resp.ByTemplate[i].Cents != resp.ByTemplate[j].Cents {
			return resp.ByTemplate[i].Cents > resp.ByTemplate[j].Cents
		}
		return resp.ByTemplate[i].TemplateName < resp.ByTemplate[j].TemplateName
	})

	// Walk every day in the window, including the empty ones, accumulating as
	// we go: a cumulative line must not jump over a payout-free stretch.
	var cash, credits int64
	var count int
	for d := windowStart; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format(dayKeyLayout)
		cash += perDayCash[key]
		credits += perDayCredits[key]
		count += perDayCount[key]
		resp.Points = append(resp.Points, payoutPoint{
			Date: key, CashCents: cash, CreditsCents: credits, Count: count,
		})
	}
	return resp
}

// payoutRowID is the primary key a Railway record is stored under. Cash
// withdrawals have their own id; credit payouts expose none, so they get a
// deterministic key from the only fields they do carry — re-syncing the same
// credit then updates its row instead of inserting a duplicate.
func payoutRowID(rec payoutRecord) string {
	if rec.ID != "" {
		return rec.ID
	}
	return fmt.Sprintf("credit:%s:%d", rec.CreatedAt.UTC().Format(time.RFC3339Nano), rec.Amount)
}

// payoutFromRecord maps a Railway history entry onto the stored row.
func payoutFromRecord(rec payoutRecord, syncedAt time.Time) Payout {
	credits := rec.Typename == railwayCreditWithdrawalTypename
	// Credit payouts carry no status field; they are settled the moment they
	// exist, so normalize them onto the cash vocabulary.
	status := rec.Status
	if status == "" {
		status = "COMPLETED"
	}
	kind := "cash"
	if credits {
		kind = "credits"
	}
	return Payout{
		ID:          payoutRowID(rec),
		CreatedAt:   rec.CreatedAt.UTC(),
		AmountCents: rec.Amount,
		Status:      status,
		Kind:        kind,
		Destination: payoutDestination(rec, credits),
		AccountID:   rec.Account.ID,
		SyncedAt:    syncedAt,
	}
}

// payoutDestination labels where a payout landed: the last four of the bank
// account or card behind the Stripe Connect destination, falling back to the
// platform name when Railway exposes neither.
func payoutDestination(rec payoutRecord, credits bool) string {
	if credits {
		return "Railway credits"
	}
	switch {
	case rec.Account.StripeConnect.BankLast4 != "":
		return "Bank ••" + rec.Account.StripeConnect.BankLast4
	case rec.Account.StripeConnect.CardLast4 != "":
		return "Card ••" + rec.Account.StripeConnect.CardLast4
	case rec.Account.Platform != "":
		// STRIPE_CONNECT reads as "Stripe connect" — sentence case, like the
		// rest of the UI.
		label := strings.ToLower(strings.ReplaceAll(rec.Account.Platform, "_", " "))
		return strings.ToUpper(label[:1]) + label[1:]
	}
	return "Unknown account"
}

// diffPayouts splits freshly fetched rows into the ones missing locally and
// the ones whose mutable fields moved (a PENDING withdrawal settling, mainly).
// Rows that are byte-identical are left alone so a sync of unchanged history
// writes nothing.
func diffPayouts(fetched []Payout, existing map[string]Payout) (inserts, updates []Payout) {
	for _, p := range fetched {
		prev, ok := existing[p.ID]
		if !ok {
			inserts = append(inserts, p)
			continue
		}
		if prev.Status != p.Status || prev.AmountCents != p.AmountCents || prev.Destination != p.Destination {
			updates = append(updates, p)
		}
	}
	return inserts, updates
}

// syncPayouts mirrors Railway's payout history into DuckDB. It is an upsert
// rather than a replace: a truncated page walk must never delete history it
// simply did not read this time.
func syncPayouts(ctx context.Context, db *gorm.DB) (inserted, updated int, err error) {
	token, customerID, err := workspaceCustomer(ctx, db)
	if err != nil {
		return 0, 0, err
	}
	records, truncated, err := getPayoutHistory(ctx, token, customerID)
	if err != nil {
		return 0, 0, err
	}
	if truncated {
		log.Printf("payout sync: history longer than the page walk, oldest payouts not read")
	}

	syncedAt := time.Now().UTC()
	fetched := make([]Payout, 0, len(records))
	for _, rec := range records {
		fetched = append(fetched, payoutFromRecord(rec, syncedAt))
	}

	stored := []Payout{}
	if err := db.WithContext(ctx).Raw(
		`SELECT id, created_at, amount_cents, status, kind, destination FROM payouts`).
		Scan(&stored).Error; err != nil {
		return 0, 0, err
	}
	existing := make(map[string]Payout, len(stored))
	for _, p := range stored {
		existing[p.ID] = p
	}

	inserts, updates := diffPayouts(fetched, existing)
	if len(inserts) > 0 {
		if err := gorm.G[Payout](db).CreateInBatches(ctx, &inserts, 100); err != nil {
			return 0, 0, err
		}
	}
	for _, p := range updates {
		if _, err := gorm.G[Payout](db).Where("id = ?", p.ID).Updates(ctx, Payout{
			Status: p.Status, AmountCents: p.AmountCents, Destination: p.Destination, SyncedAt: syncedAt,
		}); err != nil {
			return len(inserts), 0, err
		}
	}
	return len(inserts), len(updates), nil
}
