package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const templateSnapshotSpec = "@hourly"

// autoWithdrawSpec runs the payout job once a day at 08:00 UTC. ACH batches
// clear on US business mornings, so submitting in the early US morning
// (~03:00–04:00 ET) queues the request before that day's window rather than
// missing the cutoff — while still leaving the workspace token time to refresh.
const autoWithdrawSpec = "0 8 * * *"

func startCrons(db *gorm.DB) *cron.Cron {
	// Fixed to UTC so the daily withdrawal fires at a predictable wall-clock
	// time regardless of the host's timezone.
	c := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)
	if _, err := c.AddFunc(templateSnapshotSpec, func() { runTemplateSnapshots(db) }); err != nil {
		log.Fatalf("schedule template snapshots: %v", err)
	}
	if _, err := c.AddFunc(autoWithdrawSpec, func() { runAutoWithdraw(db) }); err != nil {
		log.Fatalf("schedule auto-withdraw: %v", err)
	}
	c.Start()
	// Collect once at startup so the series has a point right away instead of
	// waiting for the first tick.
	go runTemplateSnapshots(db)
	return c
}

// runTemplateSnapshots is the cron/startup entrypoint: collect and just log
// failures. The manual refresh endpoint calls collectTemplateSnapshots
// directly to report the error to the client instead.
func runTemplateSnapshots(db *gorm.DB) {
	if err := collectTemplateSnapshots(db); err != nil {
		log.Printf("template snapshots: %v", err)
	}
}

func collectTemplateSnapshots(db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	token, err := workspaceToken(ctx, db)
	if err != nil {
		return err
	}
	workspaceID, err := getProjectWorkspaceID(ctx, token)
	if err != nil {
		return err
	}
	templates, err := getWorkspaceTemplates(ctx, token, workspaceID)
	if err != nil {
		return err
	}
	if len(templates) == 0 {
		log.Printf("template snapshots: workspace %s has no templates", workspaceID)
		return nil
	}

	sampledAt := time.Now().UTC()
	snapshots := make([]TemplateSnapshot, 0, len(templates))
	for _, t := range templates {
		snapshots = append(snapshots, TemplateSnapshot{
			SampledAt:      sampledAt,
			TemplateID:     t.ID,
			Name:           t.Name,
			Code:           t.Code,
			Status:         t.Status,
			Health:         t.Health,
			Projects:       t.Projects,
			RecentProjects: t.RecentProjects,
			ActiveProjects: t.ActiveProjects,
			TotalPayout:    t.TotalPayout,
		})
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(ctx, &snapshots, 100); err != nil {
		return err
	}
	log.Printf("template snapshots: stored %d templates", len(snapshots))
	return nil
}

// autoWithdrawMu keeps runs from overlapping: the cron chain only serializes
// cron-triggered runs, not the immediate run kicked off when the user enables
// auto-withdraw, and overlapping runs could both pass the pending check and
// double-withdraw.
var autoWithdrawMu sync.Mutex

// runAutoWithdraw requests a cash payout of the available balance when the user
// has opted in. It mirrors the guardrails of the reference script: skip unless
// the balance clears the $100 minimum, skip while a withdrawal is still
// pending (idempotency), and round down to a $5 multiple before requesting.
// Safe to call directly for a one-off run; every invocation re-checks the
// guardrails.
func runAutoWithdraw(db *gorm.DB) {
	if !autoWithdrawMu.TryLock() {
		log.Printf("auto-withdraw: run already in progress, skipping")
		return
	}
	defer autoWithdrawMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s, err := loadAutoWithdrawSettings(ctx, db)
	if err != nil {
		log.Printf("auto-withdraw: load settings: %v", err)
		return
	}
	if !s.Enabled || s.WithdrawalAccountID == "" {
		return
	}

	token, customerID, err := workspaceCustomer(ctx, db)
	if err != nil {
		log.Printf("auto-withdraw: %v", err)
		return
	}

	balance, err := getAvailableBalance(ctx, token, customerID)
	if err != nil {
		log.Printf("auto-withdraw: balance: %v", err)
		return
	}
	if balance <= withdrawMinimumCents {
		log.Printf("auto-withdraw: balance $%.2f not above $%.2f minimum, skipping",
			float64(balance)/100, float64(withdrawMinimumCents)/100)
		return
	}

	pending, err := getPendingWithdrawalCount(ctx, token, customerID)
	if err != nil {
		log.Printf("auto-withdraw: pending check: %v", err)
		return
	}
	if pending > 0 {
		log.Printf("auto-withdraw: %d pending withdrawal(s), skipping", pending)
		return
	}

	// Railway rounds cash withdrawals to $5 increments — floor to a whole $5
	// multiple. Anything above the $100 minimum still floors to at least $100.
	amount := balance / 500 * 500

	if err := createCashWithdrawal(ctx, token, customerID, s.WithdrawalAccountID, amount); err != nil {
		log.Printf("auto-withdraw: create: %v", err)
		return
	}
	log.Printf("auto-withdraw: requested $%.2f withdrawal", float64(amount)/100)
}
