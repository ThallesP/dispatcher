package main

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const templateSnapshotSpec = "@hourly"

func startCrons(db *gorm.DB) *cron.Cron {
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, err := c.AddFunc(templateSnapshotSpec, func() { collectTemplateSnapshots(db) }); err != nil {
		log.Fatalf("schedule template snapshots: %v", err)
	}
	c.Start()
	// Collect once at startup so the series has a point right away instead of
	// waiting for the first tick.
	go collectTemplateSnapshots(db)
	return c
}

func collectTemplateSnapshots(db *gorm.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	token, err := workspaceToken(ctx, db)
	if err != nil {
		log.Printf("template snapshots: %v", err)
		return
	}
	workspaceID, err := getProjectWorkspaceID(ctx, token)
	if err != nil {
		log.Printf("template snapshots: %v", err)
		return
	}
	templates, err := getWorkspaceTemplates(ctx, token, workspaceID)
	if err != nil {
		log.Printf("template snapshots: %v", err)
		return
	}
	if len(templates) == 0 {
		log.Printf("template snapshots: workspace %s has no templates", workspaceID)
		return
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
		log.Printf("template snapshots: insert: %v", err)
		return
	}
	log.Printf("template snapshots: stored %d templates", len(snapshots))
}
