# Frontend: React Router SPA built to static assets in web/build/client
FROM oven/bun:1 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Backend: single Go binary that embeds the frontend build (spa.go go:embed)
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build ./web/build
# The DuckDB driver links prebuilt static libs and needs CGO
RUN CGO_ENABLED=1 go build -o /dispatcher .

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir /data
COPY --from=build /dispatcher /usr/local/bin/dispatcher
# Point DB_PATH into a mounted volume so the database survives redeploys
ENV DB_PATH=/data/dispatcher.duckdb
CMD ["dispatcher"]
