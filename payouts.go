package main

import (
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// railwayCreditWithdrawalTypename is the union arm Railway uses for payouts
// taken as Railway credits instead of cash.
const railwayCreditWithdrawalTypename = "CreditWithdrawalInfo"

// monthKeyLayout is both the bucket key and the wire format for a chart
// column: a UTC calendar month.
const monthKeyLayout = "2006-01"

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

// payout is one row of the payout table.
type payout struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	AmountCents int64     `json:"amountCents"`
	Status      string    `json:"status"`
	Kind        string    `json:"kind"`        // "cash" | "credits"
	Destination string    `json:"destination"` // "Bank ••8149", "Railway credits"
}

// payoutMonth is one column of the payout chart: what was paid out in a
// calendar month (UTC), split so cash and credits stack.
type payoutMonth struct {
	Month        string `json:"month"` // YYYY-MM
	CashCents    int64  `json:"cashCents"`
	CreditsCents int64  `json:"creditsCents"`
	Count        int    `json:"count"`
}

// payoutTotals carries the running figures the chart deliberately does not
// plot. A cumulative line over monthly columns would need a second y-scale,
// whose alignment is arbitrary and invents a correlation, so the lifetime
// number is a stat tile instead.
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

type payoutHistoryResponse struct {
	Payouts []payout      `json:"payouts"`
	Months  []payoutMonth `json:"months"`
	Totals  payoutTotals  `json:"totals"`
	// Truncated reports that the history is longer than the page walk read,
	// so the oldest months are missing from the chart.
	Truncated bool `json:"truncated"`
}

// handlePayoutHistory serves Railway's payout history live. Withdrawals are
// Railway's own immutable record, so unlike template metrics there is nothing
// to snapshot — reading them on demand can't drift.
func handlePayoutHistory(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token, customerID, err := workspaceCustomer(ctx, db)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		records, truncated, err := getPayoutHistory(ctx, token, customerID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, buildPayoutHistory(records, truncated))
	}
}

// buildPayoutHistory turns raw Railway payout records into the table rows,
// monthly chart columns and totals the dashboard renders.
func buildPayoutHistory(records []payoutRecord, truncated bool) payoutHistoryResponse {
	resp := payoutHistoryResponse{Payouts: []payout{}, Months: []payoutMonth{}, Truncated: truncated}

	// Railway returns newest first, but the page walk is the only thing
	// guaranteeing that order survives, so sort explicitly.
	sorted := slices.Clone(records)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })

	byMonth := map[string]*payoutMonth{}
	for _, rec := range sorted {
		credits := rec.Typename == railwayCreditWithdrawalTypename
		at := rec.CreatedAt.UTC()
		// Credit payouts carry no status field; they are settled the moment
		// they exist, so normalize them onto the cash vocabulary.
		status := rec.Status
		if status == "" {
			status = "COMPLETED"
		}
		kind := "cash"
		if credits {
			kind = "credits"
		}
		resp.Payouts = append(resp.Payouts, payout{
			ID:          rec.ID,
			CreatedAt:   at,
			AmountCents: rec.Amount,
			Status:      status,
			Kind:        kind,
			Destination: payoutDestination(rec, credits),
		})

		// Failed and cancelled requests stay in the table (they explain a gap)
		// but never count as money paid out.
		if voidPayoutStatuses[status] {
			continue
		}
		resp.Totals.Count++
		resp.Totals.LifetimeCents += rec.Amount
		if status == "PENDING" {
			resp.Totals.PendingCount++
			resp.Totals.PendingCents += rec.Amount
		}
		if resp.Totals.LastPayoutAt == nil || at.After(*resp.Totals.LastPayoutAt) {
			resp.Totals.LastPayoutAt = &at
		}
		if resp.Totals.FirstPayoutAt == nil || at.Before(*resp.Totals.FirstPayoutAt) {
			resp.Totals.FirstPayoutAt = &at
		}

		key := at.Format(monthKeyLayout)
		bucket, ok := byMonth[key]
		if !ok {
			bucket = &payoutMonth{Month: key}
			byMonth[key] = bucket
		}
		bucket.Count++
		if credits {
			resp.Totals.CreditsCents += rec.Amount
			bucket.CreditsCents += rec.Amount
		} else {
			resp.Totals.CashCents += rec.Amount
			bucket.CashCents += rec.Amount
		}
	}

	if len(byMonth) == 0 {
		return resp
	}
	// Zero-fill the quiet months so the columns sit on an even time axis
	// instead of collapsing a payout-free stretch into no gap at all.
	keys := slices.Sorted(maps.Keys(byMonth))
	first, _ := time.Parse(monthKeyLayout, keys[0])
	last, _ := time.Parse(monthKeyLayout, keys[len(keys)-1])
	for m := first; !m.After(last); m = m.AddDate(0, 1, 0) {
		key := m.Format(monthKeyLayout)
		if bucket, ok := byMonth[key]; ok {
			resp.Months = append(resp.Months, *bucket)
			continue
		}
		resp.Months = append(resp.Months, payoutMonth{Month: key})
	}
	return resp
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
