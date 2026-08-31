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
	auth := requireAuth(db)

	mux.HandleFunc("GET /api/health", handleHealth(db))
	mux.HandleFunc("GET /api/auth/redirect", handleAuthRedirect(db))
	mux.HandleFunc("GET /api/auth/callback", handleAuthCallback(db))
	mux.HandleFunc("POST /api/auth/cli/start", handleCLIAuthStart(db))
	mux.HandleFunc("POST /api/auth/cli/exchange", handleCLIAuthExchange)
	mux.Handle("GET /api/auth/me", auth(http.HandlerFunc(handleAuthMe)))
	mux.HandleFunc("POST /api/auth/logout", handleAuthLogout)
	mux.Handle("GET /api/analytics/payout", auth(handlePayoutSeries(db)))
	mux.Handle("GET /api/analytics/summary", auth(handleAnalyticsSummary(db)))
	mux.Handle("GET /api/analytics/templates", auth(handleTemplateAnalytics(db)))
	mux.Handle("POST /api/analytics/refresh", auth(handleRefreshAnalytics(db)))
	mux.Handle("GET /api/payouts", auth(handlePayoutHistory(db)))
	mux.Handle("GET /api/withdraw/settings", auth(handleWithdrawSettings(db)))
	mux.Handle("POST /api/withdraw/settings", auth(handleUpdateWithdrawSettings(db)))
	mux.Handle("GET /api/withdraw/accounts", auth(handleWithdrawAccounts(db)))
	mux.Handle("GET /api/notify/targets", auth(handleNotificationTargets(db)))
	mux.Handle("POST /api/notify/targets", auth(handleCreateNotificationTarget(db)))
	mux.Handle("PUT /api/notify/targets/{id}", auth(handleUpdateNotificationTarget(db)))
	mux.Handle("DELETE /api/notify/targets/{id}", auth(handleDeleteNotificationTarget(db)))
	mux.Handle("POST /api/notify/targets/{id}/test", auth(handleTestNotificationTarget(db)))
	mux.Handle("POST /api/notify/test", auth(http.HandlerFunc(handleTestNotificationDraft)))
	mux.Handle("GET /api/notify/presets", auth(http.HandlerFunc(handleNotificationPresets)))
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
