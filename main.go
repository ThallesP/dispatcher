package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "dispatcher.duckdb"
	}
	db := openDB(dsn)
	startCrons(db)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", handleHealth(db))
	mux.HandleFunc("GET /api/auth/redirect", handleAuthRedirect(db))
	mux.HandleFunc("GET /api/auth/callback", handleAuthCallback(db))
	mux.Handle("GET /api/auth/me", requireAuth(http.HandlerFunc(handleAuthMe)))
	mux.HandleFunc("POST /api/auth/logout", handleAuthLogout)
	mux.Handle("GET /api/analytics/payout", requireAuth(handlePayoutSeries(db)))
	mux.Handle("GET /api/analytics/summary", requireAuth(handleAnalyticsSummary(db)))
	mux.Handle("GET /api/analytics/templates", requireAuth(handleTemplateAnalytics(db)))
	mux.Handle("POST /api/analytics/refresh", requireAuth(handleRefreshAnalytics(db)))
	mux.Handle("GET /api/withdraw/settings", requireAuth(handleWithdrawSettings(db)))
	mux.Handle("POST /api/withdraw/settings", requireAuth(handleUpdateWithdrawSettings(db)))
	mux.Handle("GET /api/withdraw/accounts", requireAuth(handleWithdrawAccounts(db)))
	mux.Handle("/", spaHandler())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
	log.Printf("listening on http://localhost:%s", port)
	log.Fatal(server.ListenAndServe())
}
