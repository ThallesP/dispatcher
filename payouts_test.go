package main

import (
	"testing"
	"time"
)

// cashRecord builds a Withdrawal-arm Railway record with a bank destination.
func cashRecord(at time.Time, cents int64, status, bankLast4 string) payoutRecord {
	rec := payoutRecord{Typename: "Withdrawal", ID: "w-" + at.Format(time.RFC3339), Amount: cents, Status: status, CreatedAt: at}
	rec.Account.ID = "acct-1"
	rec.Account.Platform = "STRIPE_CONNECT"
	rec.Account.StripeConnect.BankLast4 = bankLast4
	return rec
}

// creditRecord builds a CreditWithdrawalInfo-arm record, which Railway returns
// with only an amount and a date.
func creditRecord(at time.Time, cents int64) payoutRecord {
	return payoutRecord{Typename: railwayCreditWithdrawalTypename, Amount: cents, CreatedAt: at}
}

func cashPayout(at time.Time, cents int64, status string) Payout {
	return Payout{ID: "w-" + at.Format(time.RFC3339Nano), CreatedAt: at, AmountCents: cents, Status: status, Kind: "cash", Destination: "Bank ••8149"}
}

func TestBuildPayoutHistoryAccumulatesAcrossTheWindow(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 8, d, 9, 0, 0, 0, time.UTC) }

	// A 7-day window covers Aug 24..30. Aug 20 falls in the previous window,
	// and Aug 25-27 are payout-free so the line must hold its level there.
	got := buildPayoutHistory([]Payout{
		cashPayout(day(20), 40000, "COMPLETED"), // previous window
		cashPayout(day(24), 10000, "COMPLETED"),
		cashPayout(day(28), 15000, "COMPLETED"),
		cashPayout(day(30), 12000, "PENDING"),
	}, 7, now)

	if len(got.Points) != 7 {
		t.Fatalf("points = %d, want 7", len(got.Points))
	}
	if first := got.Points[0]; first.Date != "2026-08-24" || first.CashCents != 10000 || first.Count != 1 {
		t.Errorf("points[0] = %+v, want Aug 24 / 10000 / 1", first)
	}
	// The three quiet days hold the running total rather than dropping to zero.
	for i, date := range []string{"2026-08-25", "2026-08-26", "2026-08-27"} {
		p := got.Points[i+1]
		if p.Date != date || p.CashCents != 10000 || p.Count != 1 {
			t.Errorf("points[%d] = %+v, want %s holding 10000 / 1", i+1, p, date)
		}
	}
	if last := got.Points[6]; last.Date != "2026-08-30" || last.CashCents != 37000 || last.Count != 3 {
		t.Errorf("points[last] = %+v, want Aug 30 / 37000 / 3", last)
	}

	want := payoutWindow{Days: 7, TotalCents: 37000, PreviousCents: 40000, Count: 3}
	gotWindow := got.Window
	if gotWindow.ChangePct == nil {
		t.Fatalf("window.ChangePct = nil, want a comparison against the previous 7d")
	}
	if pct := *gotWindow.ChangePct; pct > -7.4 || pct < -7.6 {
		t.Errorf("window.ChangePct = %v, want ≈-7.5%%", pct)
	}
	gotWindow.ChangePct = nil
	if gotWindow != want {
		t.Errorf("window = %+v, want %+v", gotWindow, want)
	}

	// Lifetime spans everything, including the payout outside the window.
	if got.Totals.LifetimeCents != 77000 || got.Totals.Count != 4 {
		t.Errorf("totals = %+v, want lifetime 77000 across 4 payouts", got.Totals)
	}
	if got.Totals.PendingCents != 12000 || got.Totals.PendingCount != 1 {
		t.Errorf("pending = %d/%d, want 12000/1", got.Totals.PendingCents, got.Totals.PendingCount)
	}
}

func TestBuildPayoutHistoryExcludesVoidPayoutsButStillListsThem(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	at := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)

	got := buildPayoutHistory([]Payout{
		cashPayout(at, 99900, "FAILED"),
		cashPayout(at.Add(-time.Hour), 10000, "COMPLETED"),
	}, 7, now)

	// The failed request explains a gap, so it stays in the table...
	if len(got.Payouts) != 2 {
		t.Fatalf("payouts = %d, want both rows listed", len(got.Payouts))
	}
	if got.Payouts[0].Status != "FAILED" {
		t.Errorf("payouts[0].Status = %q, want the newest (failed) row first", got.Payouts[0].Status)
	}
	// ...but no money moved, so it counts nowhere.
	if got.Window.TotalCents != 10000 || got.Totals.LifetimeCents != 10000 || got.Window.Count != 1 {
		t.Errorf("window %+v / totals %+v, want only the completed 10000 counted", got.Window, got.Totals)
	}
}

func TestBuildPayoutHistoryBreaksTheWindowDownByTemplate(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	credit := func(hoursAgo int, cents int64, status, templateID, name string) Payout {
		at := now.Add(-time.Duration(hoursAgo) * time.Hour)
		return Payout{ID: "c-" + at.Format(time.RFC3339Nano), CreatedAt: at, AmountCents: cents, Status: status, Kind: "credits", Destination: "Railway credits", TemplateID: templateID, TemplateName: name}
	}
	payouts := []Payout{
		credit(1, 125, "COMPLETED", "twenty", "Twenty CRM"), credit(2, 13, "COMPLETED", "twenty", "Twenty CRM"),
		credit(3, 204, "COMPLETED", "owncast", "Owncast"), credit(4, 5, "COMPLETED", "", ""),
		credit(5, 3, "COMPLETED", unattributedTemplateID, ""),
		credit(6, 50, "FAILED", "owncast", "Owncast"),           // void: not counted anywhere
		credit(24*40, 999, "COMPLETED", "twenty", "Twenty CRM"), // outside the window
		cashPayout(now.Add(-7*time.Hour), 10000, "COMPLETED"),   // cash: never attributed
	}

	got := buildPayoutHistory(payouts, 30, now)

	want := []payoutTemplateTotal{
		{TemplateID: "owncast", TemplateName: "Owncast", Count: 1, Cents: 204},
		{TemplateID: "twenty", TemplateName: "Twenty CRM", Count: 2, Cents: 138},
		{TemplateID: "pending", TemplateName: "pending", Count: 1, Cents: 5},
		{TemplateID: unattributedTemplateID, TemplateName: unattributedTemplateID, Count: 1, Cents: 3},
	}
	if len(got.ByTemplate) != len(want) {
		t.Fatalf("byTemplate = %+v, want %+v", got.ByTemplate, want)
	}
	for i := range want {
		if got.ByTemplate[i] != want[i] {
			t.Errorf("byTemplate[%d] = %+v, want %+v", i, got.ByTemplate[i], want[i])
		}
	}
}

func TestBuildPayoutHistoryCapsTableRows(t *testing.T) {
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	payouts := []Payout{}
	for i := range 20 {
		payouts = append(payouts, cashPayout(now.Add(-time.Duration(i)*time.Hour), 10000, "COMPLETED"))
	}

	got := buildPayoutHistory(payouts, 30, now)

	if len(got.Payouts) != recentPayoutRows {
		t.Errorf("payouts = %d, want capped at %d", len(got.Payouts), recentPayoutRows)
	}
	if got.TotalRows != 20 {
		t.Errorf("totalRows = %d, want 20 so the table can say what it hides", got.TotalRows)
	}
	// The cap must not touch the aggregates.
	if got.Totals.Count != 20 || got.Window.Count != 20 {
		t.Errorf("counts = %d/%d, want all 20 counted despite the row cap", got.Totals.Count, got.Window.Count)
	}
}

func TestPayoutFromRecordNormalizesCreditsAndDestinations(t *testing.T) {
	at := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	syncedAt := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)

	// Credits arrive with no id and no status: they need a deterministic key
	// so a re-sync updates rather than duplicates, and settle on creation.
	credit := payoutFromRecord(creditRecord(at, 2500), syncedAt)
	if credit.Kind != "credits" || credit.Status != "COMPLETED" || credit.Destination != "Railway credits" {
		t.Errorf("credit = %+v, want kind=credits status=COMPLETED destination=Railway credits", credit)
	}
	if again := payoutFromRecord(creditRecord(at, 2500), syncedAt.Add(time.Hour)); again.ID != credit.ID {
		t.Errorf("credit id = %q then %q, want a stable synthetic key", credit.ID, again.ID)
	}
	if other := payoutFromRecord(creditRecord(at, 9900), syncedAt); other.ID == credit.ID {
		t.Errorf("two different credits share id %q", other.ID)
	}

	card := cashRecord(at, 10000, "COMPLETED", "")
	card.Account.StripeConnect.CardLast4 = "4242"
	bare := cashRecord(at, 10000, "COMPLETED", "")
	bare.Account.StripeConnect.CardLast4 = ""

	for _, tc := range []struct {
		name string
		rec  payoutRecord
		want string
	}{
		{"bank", cashRecord(at, 10000, "COMPLETED", "8149"), "Bank ••8149"},
		{"card", card, "Card ••4242"},
		{"platform only", bare, "Stripe connect"},
	} {
		if got := payoutDestination(tc.rec, false); got != tc.want {
			t.Errorf("%s destination = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDiffPayoutsOnlyWritesWhatChanged(t *testing.T) {
	at := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	settled := cashPayout(at, 10000, "COMPLETED")
	pending := cashPayout(at.Add(time.Hour), 15000, "PENDING")
	fresh := cashPayout(at.Add(2*time.Hour), 12000, "COMPLETED")

	// The pending row has since settled; the completed one is untouched.
	nowSettled := pending
	nowSettled.Status = "COMPLETED"

	inserts, updates := diffPayouts(
		[]Payout{settled, nowSettled, fresh},
		map[string]Payout{settled.ID: settled, pending.ID: pending},
	)

	if len(inserts) != 1 || inserts[0].ID != fresh.ID {
		t.Errorf("inserts = %+v, want only the unseen payout", inserts)
	}
	if len(updates) != 1 || updates[0].ID != pending.ID || updates[0].Status != "COMPLETED" {
		t.Errorf("updates = %+v, want only the pending row settling", updates)
	}
}
