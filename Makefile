# EVCC Trip Planner — development commands. Run `make help` for a list.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO_MIN   := 1.27
NODE_MIN := 22.12
BIN      := bin/evcc-planner

.PHONY: help setup dev dev-live dev-reset build build-web build-go run verify test test-go test-web e2e lint fmt test-live docker-smoke clean

help: ## List available commands
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-13s %s\n", $$1, $$2}'

setup: ## Check Go/Node prerequisites; install locked dependencies and the test browser
	@command -v go >/dev/null || { echo "Go $(GO_MIN)+ is required: https://go.dev/dl/"; exit 1; }
	@command -v node >/dev/null || { echo "Node $(NODE_MIN)+ is required: https://nodejs.org/"; exit 1; }
	@command -v pnpm >/dev/null || { echo "pnpm is required: npm install -g pnpm (or corepack enable)"; exit 1; }
	@go version | awk '{v=substr($$3,3); split(v,a,"."); if (a[1]<1 || (a[1]==1 && a[2]<27)) {print "Go $(GO_MIN)+ required, found " v; exit 1}}'
	@node -e 'const [a,b]=process.versions.node.split(".").map(Number); if (a<22||(a===22&&b<12)) {console.error("Node $(NODE_MIN)+ required, found "+process.versions.node); process.exit(1)}'
	go mod download
	cd web && pnpm install --frozen-lockfile
	@cd web && if ! pnpm exec playwright install chromium; then \
	  if [ "$$(uname)" = "Darwin" ]; then \
	    echo "Retrying the browser download with the macOS keychain certificates (TLS-inspecting networks)"; \
	    ca=$$(mktemp); security find-certificate -a -p /Library/Keychains/System.keychain \
	      /System/Library/Keychains/SystemRootCertificates.keychain > "$$ca"; \
	    NODE_EXTRA_CA_CERTS="$$ca" pnpm exec playwright install chromium; rm -f "$$ca"; \
	  else exit 1; fi; \
	fi
	@echo "Setup complete. Next: make dev"

dev: ## Run the app locally with seeded demo data and stubs (UI: http://127.0.0.1:7201)
	@go build -o bin/devrun ./cmd/devrun
	@./bin/devrun

dev-live: ## Like dev, but with your .env (real EVCC, calendar, and routes)
	@go build -o bin/devrun ./cmd/devrun
	@./bin/devrun -demo=false

dev-reset: ## Delete the local demo database (reseeded on the next make dev)
	rm -f data/demo.db data/demo.db-shm data/demo.db-wal

build: build-web build-go ## Production build: compiled UI and the Go binary

build-web:
	cd web && pnpm build

build-go:
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/evcc-planner

run: build ## Run the production build locally with your real config
	./$(BIN)

verify: ## Formatting, lint, type checks, unit/API/component tests, production build, browser tests
	@echo "==> Go formatting"
	@unformatted=$$(gofmt -l cmd internal); if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "Run: gofmt -w cmd internal"; exit 1; fi
	@echo "==> go vet"
	@go vet ./...
	@echo "==> Go tests (unit and API)"
	@go test ./...
	@echo "==> Web formatting"
	@cd web && pnpm --silent format:check
	@echo "==> Web lint"
	@cd web && pnpm --silent lint
	@echo "==> Web type check"
	@cd web && pnpm --silent typecheck
	@echo "==> Web unit and component tests"
	@cd web && pnpm --silent test
	@echo "==> Production build"
	@$(MAKE) --no-print-directory build
	@echo "==> Browser tests (report: web/playwright-report/index.html)"
	@cd web && pnpm --silent e2e
	@echo "All checks passed."

test: test-go test-web ## Fast tests only (no build, no browser)

test-go:
	go test ./...

test-web:
	cd web && pnpm test

e2e: build ## Browser tests against the production build
	cd web && pnpm e2e

lint: ## Linters and type checks only
	go vet ./...
	cd web && pnpm lint && pnpm typecheck

fmt: ## Format Go and web sources
	gofmt -w cmd internal
	cd web && pnpm format

test-live: ## Read-only checks against your real EVCC and forecast sources (.env)
	go test -tags live -count=1 -v -run Live ./internal/evcc/ ./internal/forecast/

docker-smoke: ## Build the production image, start it, and check health, UI, size, and memory
	./scripts/docker-smoke.sh

clean: ## Remove build output and test artifacts (keeps data/)
	rm -rf bin web/dist web/test-results web/playwright-report
