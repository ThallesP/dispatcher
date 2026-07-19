package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const (
	templateSnapshotSpec = "@hourly"
	weeklySummarySpec    = "0 9 * * MON"
)

// defaultAutoWithdrawSpec runs the payout job once a day at 08:00 UTC. ACH
// batches clear on US business mornings, so submitting in the early US morning
// (~03:00–04:00 ET) queues the request before that day's window rather than
// missing the cutoff — while still leaving the workspace token time to refresh.
// Users can override it per-workspace via the settings API.
const defaultAutoWithdrawSpec = "0 8 * * *"

// autoWithdrawEntry tracks the live cron registration of the payout job so a
// settings change can swap the schedule without restarting the process.
var autoWithdrawEntry struct {
	sync.Mutex
	c  *cron.Cron
	id cron.EntryID
}

// scheduleAutoWithdraw (re)registers the auto-withdraw job under the given
// cron spec, replacing the previous registration. The spec is parsed with the
// same standard parser the API validates against, so an error here means the
// stored value predates validation.
func scheduleAutoWithdraw(db *gorm.DB, spec string) error {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return err
	}
	autoWithdrawEntry.Lock()
	defer autoWithdrawEntry.Unlock()
	if autoWithdrawEntry.id != 0 {
		autoWithdrawEntry.c.Remove(autoWithdrawEntry.id)
	}
	autoWithdrawEntry.id = autoWithdrawEntry.c.Schedule(schedule, cron.FuncJob(func() { runAutoWithdraw(db) }))
	return nil
}

func startCrons(db *gorm.DB) *cron.Cron {
	// Fixed to UTC so withdrawal schedules fire at a predictable wall-clock
	// time regardless of the host's timezone.
	c := cron.New(
		cron.WithLocation(time.UTC),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)
	if _, err := c.AddFunc(templateSnapshotSpec, func() { runTemplateSnapshots(db) }); err != nil {
		log.Fatalf("schedule template snapshots: %v", err)
	}
	if _, err := c.AddFunc(weeklySummarySpec, func() { runWeeklySummary(db) }); err != nil {
		log.Fatalf("schedule weekly summary: %v", err)
	}
	autoWithdrawEntry.c = c

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	s, err := loadAutoWithdrawSettings(ctx, db)
	cancel()
	if err != nil {
		log.Fatalf("load auto-withdraw settings: %v", err)
	}
	if err := scheduleAutoWithdraw(db, s.Schedule); err != nil {
		log.Printf("auto-withdraw: stored schedule %q invalid (%v), using default", s.Schedule, err)
		if err := scheduleAutoWithdraw(db, defaultAutoWithdrawSpec); err != nil {
			log.Fatalf("schedule auto-withdraw: %v", err)
		}
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

func runWeeklySummary(db *gorm.DB) {
	ev, ok, err := loadWeeklySummary(db, time.Now().UTC())
	if err != nil {
		log.Printf("weekly summary: %v", err)
		return
	}
	if ok {
		go notifyAll(db, ev)
	}
}

// All snapshot entrypoints share this collector. Serializing it prevents a
// startup/manual run from observing the same previous health as the cron and
// emitting a duplicate threshold crossing.
var templateSnapshotMu sync.Mutex

func collectTemplateSnapshots(db *gorm.DB) error {
	templateSnapshotMu.Lock()
	defer templateSnapshotMu.Unlock()

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
	previous := make(map[string]TemplateSnapshot, len(templates))
	for _, t := range templates {
		snapshot, err := gorm.G[TemplateSnapshot](db).
			Where("template_id = ?", t.ID).
			Order("sampled_at DESC, id DESC").
			First(ctx)
		if err == nil {
			previous[t.ID] = snapshot
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	snapshots := make([]TemplateSnapshot, 0, len(templates))
	for _, t := range templates {
		snapshot := TemplateSnapshot{
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
		}
		if sh := t.SupportHealth; sh != nil {
			snapshot.SupportSolved = sh.Solved
			snapshot.SupportCsat = sh.Csat
			snapshot.SupportHealth = sh.AggregateHealth
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := gorm.G[TemplateSnapshot](db).CreateInBatches(ctx, &snapshots, 100); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		prev, ok := previous[snapshot.TemplateID]
		if !ok || !crossedHealthThreshold(prev.Health, snapshot.Health) {
			continue
		}
		go notifyAll(db, healthDropEvent(snapshot, *prev.Health))
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
	formattedAmount := fmt.Sprintf("$%.2f", float64(amount)/100)
	go notifyAll(db, NotificationEvent{
		Event:      "payout",
		Title:      "Withdrawal requested",
		Message:    fmt.Sprintf("Requested a %s withdrawal from your Railway balance.", formattedAmount),
		OccurredAt: time.Now().UTC(),
		Data: PayoutNotificationData{
			AmountCents: amount,
			Amount:      formattedAmount,
			AccountID:   s.WithdrawalAccountID,
		},
	})
}
