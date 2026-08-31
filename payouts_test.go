package main

import (
	"testing"
	"time"
)

// cashPayout builds a Withdrawal-arm record with a bank destination.
func cashPayout(at time.Time, cents int64, status, bankLast4 string) payoutRecord {
	rec := payoutRecord{Typename: "Withdrawal", ID: at.Format(time.RFC3339), Amount: cents, Status: status, CreatedAt: at}
	rec.Account.Platform = "STRIPE_CONNECT"
	rec.Account.StripeConnect.BankLast4 = bankLast4
	return rec
}

// creditPayout builds a CreditWithdrawalInfo-arm record, which Railway returns
// with only an amount and a date.
func creditPayout(at time.Time, cents int64) payoutRecord {
	return payoutRecord{Typename: railwayCreditWithdrawalTypename, Amount: cents, CreatedAt: at}
}

func TestBuildPayoutHistoryBucketsMonthsAndTotals(t *testing.T) {
	may := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	aug := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)

	// Deliberately out of order: the walk's ordering must not be trusted.
	// June and July have no payouts and must still appear as empty columns.
	got := buildPayoutHistory([]payoutRecord{
		cashPayout(aug, 10000, "COMPLETED", "8149"),
		cashPayout(may, 10000, "COMPLETED", "8149"),
		creditPayout(may.Add(time.Hour), 2500),
		cashPayout(aug.Add(time.Hour), 15000, "PENDING", "8149"),
		cashPayout(aug.Add(2*time.Hour), 99900, "FAILED", "8149"),
	}, false)

	// Newest first for the table, and the failed request stays visible.
	if len(got.Payouts) != 5 {
		t.Fatalf("payouts = %d, want 5", len(got.Payouts))
	}
	if got.Payouts[0].AmountCents != 99900 || got.Payouts[0].Status != "FAILED" {
		t.Errorf("payouts[0] = %+v, want the newest (failed) request first", got.Payouts[0])
	}
	if last := got.Payouts[len(got.Payouts)-1]; last.AmountCents != 10000 || !last.CreatedAt.Equal(may) {
		t.Errorf("payouts[last] = %+v, want the oldest May payout", last)
	}

	wantMonths := []payoutMonth{
		{Month: "2026-05", CashCents: 10000, CreditsCents: 2500, Count: 2},
		{Month: "2026-06"},
		{Month: "2026-07"},
		{Month: "2026-08", CashCents: 25000, Count: 2},
	}
	if len(got.Months) != len(wantMonths) {
		t.Fatalf("months = %v, want %v", got.Months, wantMonths)
	}
	for i, want := range wantMonths {
		if got.Months[i] != want {
			t.Errorf("months[%d] = %+v, want %+v", i, got.Months[i], want)
		}
	}

	// The failed request is excluded from every total; the pending one counts
	// toward the lifetime figure and is also called out on its own.
	want := payoutTotals{
		LifetimeCents: 37500, CashCents: 35000, CreditsCents: 2500,
		PendingCents: 15000, PendingCount: 1, Count: 4,
	}
	got.Totals.FirstPayoutAt, got.Totals.LastPayoutAt = nil, nil
	if got.Totals != want {
		t.Errorf("totals = %+v, want %+v", got.Totals, want)
	}
}

func TestBuildPayoutHistoryNormalizesCreditsAndDestinations(t *testing.T) {
	at := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	got := buildPayoutHistory([]payoutRecord{creditPayout(at, 2500)}, false)

	// Credits arrive with no status field; they are settled on arrival.
	p := got.Payouts[0]
	if p.Kind != "credits" || p.Status != "COMPLETED" || p.Destination != "Railway credits" {
		t.Errorf("credit payout = %+v, want kind=credits status=COMPLETED destination=Railway credits", p)
	}

	card := cashPayout(at, 10000, "COMPLETED", "")
	card.Account.StripeConnect.CardLast4 = "4242"
	bare := cashPayout(at, 10000, "COMPLETED", "")

	for _, tc := range []struct {
		name string
		rec  payoutRecord
		want string
	}{
		{"bank", cashPayout(at, 10000, "COMPLETED", "8149"), "Bank ••8149"},
		{"card", card, "Card ••4242"},
		{"platform only", bare, "Stripe connect"},
	} {
		if got := payoutDestination(tc.rec, false); got != tc.want {
			t.Errorf("%s destination = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildPayoutHistoryEmpty(t *testing.T) {
	got := buildPayoutHistory(nil, false)
	// Non-nil slices so the JSON is [] rather than null.
	if got.Payouts == nil || got.Months == nil {
		t.Fatalf("empty history = %+v, want empty slices", got)
	}
	if len(got.Payouts) != 0 || len(got.Months) != 0 || got.Totals.LifetimeCents != 0 {
		t.Errorf("empty history = %+v, want zero everything", got)
	}
}
