# Dispatcher

Go API + React Router SPA, shipped as a single binary.

- `main.go` — wiring: env, database, routes, server.
- `handlers.go` — `/api/*` handlers.
- `db.go` — GORM models and connection.
- `spa.go` — serves the built frontend (embedded via `go:embed`), with an `index.html` fallback for client-side routes.
- `web/` — React Router v7 app in SPA mode (Vite is its build tool; it outputs static files to `web/build/client`).

## Development

Two terminals:

```sh
make dev-api   # Go API on http://localhost:8090
make dev-web   # frontend on http://localhost:5173, proxies /api to the Go server
```

Work against http://localhost:5173 — you get Vite HMR, and API calls hit Go.

## Production

```sh
make build     # npm run build, then go build (frontend embedded)
./dispatcher   # serves everything on :8090 (override with PORT)
```

Note: `go build` embeds `web/build/client`, so the frontend must be built first — `make build` handles the order.

## Database

GORM with DuckDB (via `github.com/vogo/duckdb/v2`). Data lives in `dispatcher.duckdb` next to the binary (override with `DB_PATH`); `db.go` holds the models and connection, and `AutoMigrate` runs on startup. Building needs CGO (DuckDB links a C library) — already the Go default.

## Notifications

Notification targets send payout requests, template health drops, and weekly template summaries to Discord, Slack, ntfy, or a custom HTTP webhook. Targets use editable Go text/templates and can be tested before they are enabled.

Background collection, auto-withdraw, weekly summaries, and notification delivery assume the app runs as a single process. Running multiple replicas can duplicate cron work and notifications.

## Adding things

- **API route**: register another `mux.HandleFunc("GET /api/...")` in `main.go`, implement it in `handlers.go`.
- **Model**: add a struct in `db.go` and list it in `AutoMigrate`.
- **Page**: add a file under `web/src/routes/` and register it in `web/src/routes.ts`.
