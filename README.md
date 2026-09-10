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

## Sessions

The auth cookie carries the whole Railway OAuth grant — access token, refresh
token, expiry — sealed with AES-GCM under the OAuth client secret. There is no
session table: Railway stays the only authority on who you are.

A Railway access token lasts about an hour, which is how long a login used to
last. `requireAuth` now refreshes the grant when the access token is spent,
re-seals the cookie, and carries on, so a login lives for as long as Railway
honours the refresh token. Parallel requests that arrive on an expired token
share a single refresh. Every request is still validated against Railway, so
losing workspace access ends the session at once, and the refreshed token is
also written to the stored workspace credentials that background jobs use.

The cookie is encrypted rather than signed so a leaked cookie cannot be
replayed against Railway's own API, and it is `HttpOnly`, `SameSite=Lax`, and
`Secure` whenever `CALLBACK_URL` is HTTPS. Its lifetime is 400 days, the
ceiling browsers accept; `dispatcherctl` stores the same value and adopts the
renewed session whenever Dispatcher rotates it. Logging out drops the cookie,
and re-registering the OAuth client invalidates every session, since the client
secret is the encryption key.

## Notifications

Notification targets send payout requests, template health drops, and weekly template summaries to Discord, Slack, ntfy, or a custom HTTP webhook. Targets use editable Go text/templates and can be tested before they are enabled.

### Which template earned each payout

Railway's payout ledger says how much each kickback credit was worth but not
which template earned it. Dispatcher works it out: every hour it snapshots
each template's lifetime earnings, and the credit payouts that arrive between
two snapshots must sum, per template, to that template's earnings delta. The
collector matches new payouts to templates by finding the partition of the
window's payouts that reproduces those deltas, and stores the result on the
payout row (`templateId`, `templateName` in `/api/payouts` and
`dispatcherctl payouts`, a Template column in the payouts table). Payouts
older than the first snapshot can never be matched and are marked
`untracked` ("before tracking"); a payout the snapshots cannot explain yet
stays `pending` and is retried after each snapshot, and is marked `unknown`
after two days. Cash withdrawals are lump sums of the balance and are never
attributed.

Background collection, auto-withdraw, weekly summaries, and notification delivery assume the app runs as a single process. Running multiple replicas can duplicate cron work and notifications.

## CLI

`dispatcherctl` is a small, standalone HTTP client intended for agents and
scripts. It queries a running Dispatcher instance, never its DuckDB file or
Railway directly. Authenticated commands use the same Railway OAuth flow and
session as the browser app; there are no separate API tokens. The health check
remains public just like `/api/health`.

Install the latest CLI release on Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/ThallesP/dispatcher/main/install.sh | sh
```

The installer verifies the release checksum and installs to `/usr/local/bin`
when it is writable, otherwise to `~/.local/bin`. Set
`DISPATCHER_INSTALL_DIR` to choose another directory, or
`DISPATCHER_VERSION=v0.1.0` to install a specific release.

You can also build or install from source:

```sh
make build-cli
# Or: make install-cli

./dispatcherctl login       # asks for the Dispatcher URL, then opens Railway OAuth
./dispatcherctl whoami

./dispatcherctl summary
./dispatcherctl templates
./dispatcherctl payouts --days 90
./dispatcherctl notifications
./dispatcherctl withdraw-settings
./dispatcherctl withdraw-accounts
```

The first `login` asks for the Dispatcher instance URL and saves it after OAuth
succeeds. Later commands and logins reuse that URL automatically. `--url` and
`DISPATCHER_URL` override the saved instance; a successful login through an
override makes it the new default.

During login Dispatcher creates a short-lived pending login and returns a
Railway authorization URL. The CLI opens that URL (or prints it in a headless
environment) and polls Dispatcher while the browser completes the existing
OAuth callback. Browser and CLI login share the same Railway authorization URL
builder, code exchange, workspace-access check, saved credentials, and
`requireAuth` cookie middleware. The only CLI-specific part is how the completed
session reaches the terminal. Sessions renew themselves, so a CLI login lasts
until Railway revokes the grant. That result is bound to a verifier held only by
the CLI, so the browser never receives the Railway session itself. This also
works when the CLI and browser are on different machines. Sessions are stored
with owner-only permissions in the operating system's user config directory.
Use `dispatcherctl logout` to remove the session for an instance.

Responses are JSON and are pretty-printed by default. Pass `--compact` before
the command for machine-friendly JSONL output. `get` makes it possible to query
new read endpoints without waiting for a CLI release:

```sh
./dispatcherctl --compact get analytics/templates
```

Run `dispatcherctl help` for the full command list. A remote Dispatcher URL must
use HTTPS for login; plain HTTP is accepted only for loopback development. Set
`DISPATCHER_CONFIG` or pass `--config` to override the credentials-file path.

To stamp a release version into the binary:

```sh
make build-cli CLI_VERSION=v0.1.0
./dispatcherctl version
```

Pushing a `v*` tag runs the CLI release workflow and publishes the archives
used by the installer.

## CDN and caching

Railway's CDN sits in front of the service, and it caches from the origin's own
headers — the cache rules live in `spa.go`, not in a dashboard:

| Response | `Cache-Control` | Why |
| --- | --- | --- |
| `/assets/*` | `public, max-age=31536000, immutable` | Vite fingerprints the filename, so a URL's content never changes; a deploy publishes new URLs |
| `index.html` (incl. the client-route fallback) | `no-cache` | The shell keeps its URL across deploys, so it must be revalidated or it would keep pointing at deleted assets |
| `favicon.*` and other unhashed files | `public, max-age=3600, stale-while-revalidate=86400` | Same URL across deploys, but stale for an hour is harmless |
| `/api/*` | `no-store` | Per-user data behind the session cookie, plus OAuth redirects |
| Missing files | `no-store` | A 404 from a bad deploy should not be pinned at the edge |

Every static response also carries an ETag hashed from its bytes at startup, so
a revalidation costs a 304 instead of a re-download.

Two rules the SPA fallback depends on: `/analytics` and friends answer with the
shell or a 404 depending on `Accept`, so those responses send `Vary: Accept`;
and the shell is only ever `no-cache`, which is what keeps a deploy from
serving an old `index.html` that references assets the new build removed. A
purge is not needed on deploy — asset URLs change and the shell revalidates.

## Adding things

- **API route**: register another `mux.HandleFunc("GET /api/...")` in `main.go`, implement it in `handlers.go`.
- **Model**: add a struct in `db.go` and list it in `AutoMigrate`.
- **Page**: add a file under `web/src/routes/` and register it in `web/src/routes.ts`.
