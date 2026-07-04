package main

import (
	"context"
	"fmt"
	"log"
	"time"

	duckdb "github.com/vogo/duckdb/v2"
	"gorm.io/gorm"
)

type RailwayCredentials struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ClientID       string    `json:"clientId"`
	ClientSecret   string    `json:"clientSecret"`
	AccessToken    string    `json:"accessToken"`
	RefreshToken   string    `json:"refreshToken"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt"`
	CreatedAt      time.Time `json:"createdAt"`
}

// TemplateSnapshot is one point-in-time observation of a workspace template,
// appended on every poll so payout/usage can be charted over time.
type TemplateSnapshot struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	SampledAt      time.Time `json:"sampledAt"`
	TemplateID     string    `json:"templateId"`
	Name           string    `json:"name"`
	Code           string    `json:"code"`
	Status         string    `json:"status"`
	Health         *float64  `json:"health"`
	Projects       int64     `json:"projects"`
	RecentProjects int64     `json:"recentProjects"`
	ActiveProjects int64     `json:"activeProjects"`
	TotalPayout    float64   `json:"totalPayout"`
}

func openDB(dsn string) *gorm.DB {
	db, err := gorm.Open(duckdb.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("open database %s: %v", dsn, err)
	}
	if err := db.AutoMigrate(&RailwayCredentials{}, &TemplateSnapshot{}); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	// AutoMigrate re-alters columns on every startup, and DuckDB cannot
	// replay ALTER TABLE entries from the WAL (internal GetDefaultDatabase
	// error). Checkpoint so they never survive an unclean shutdown.
	if err := db.Exec("CHECKPOINT").Error; err != nil {
		log.Fatalf("checkpoint database: %v", err)
	}
	return db
}

func saveToken(ctx context.Context, db *gorm.DB, id uint, tok tokenResponse) error {
	// Updates skips zero-value fields, so a refresh response without a
	// rotated refresh_token keeps the stored one.
	_, err := gorm.G[RailwayCredentials](db).Where("id = ?", id).Updates(ctx, RailwayCredentials{
		AccessToken:    tok.AccessToken,
		RefreshToken:   tok.RefreshToken,
		TokenExpiresAt: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
	})
	return err
}

// workspaceToken returns a valid access token for background work,
// refreshing and persisting it when the stored one is near expiry.
func workspaceToken(ctx context.Context, db *gorm.DB) (string, error) {
	creds, err := gorm.G[RailwayCredentials](db).First(ctx)
	if err != nil {
		return "", fmt.Errorf("load railway credentials: %w", err)
	}
	if creds.AccessToken != "" && time.Now().Before(creds.TokenExpiresAt.Add(-time.Minute)) {
		return creds.AccessToken, nil
	}
	if creds.RefreshToken == "" {
		return "", fmt.Errorf("no token stored, complete login at /api/auth/redirect")
	}
	tok, err := refreshAccessToken(ctx, creds)
	if err != nil {
		return "", err
	}
	if err := saveToken(ctx, db, creds.ID, tok); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}
