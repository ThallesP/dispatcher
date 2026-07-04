.PHONY: dev-api dev-web build run clean

# Terminal 1: Go API on :8090
# The placeholder file keeps go:embed happy before the first frontend build.
dev-api:
	@mkdir -p web/build/client && touch web/build/client/.keep
	go run .

# Terminal 2: Vite dev server on :5173, proxies /api to :8090
dev-web:
	cd web && npm run dev

# Build the frontend, then compile it into a single Go binary
build:
	cd web && npm run build
	go build -o dispatcher .

run: build
	./dispatcher

clean:
	rm -rf dispatcher web/build
