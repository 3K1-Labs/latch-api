##
## Latch Backend — Makefile
##
## Usage: make <target>
##

.DEFAULT_GOAL := help
.PHONY: help run build test lint sqlc swag tidy \
        migrate-up migrate-down migrate-version migrate-force migrate-create \
        docker-up docker-down docker-logs docker-build \
        install-tools clean

# ── Variables ────────────────────────────────────────────────────────────────

BINARY      := ./bin/latch-backend
SERVER_PKG  := ./cmd/server
MIGRATE_PKG := ./cmd/migrate
GO_FILES    := $(shell find . -name '*.go' -not -path './vendor/*' -not -path './tmp/*')

# Load DATABASE_URL from .env if available (for local migrate targets)
-include .env
export

# ── Development ───────────────────────────────────────────────────────────────

## run: Start the server with air live-reload (requires air in PATH)
run:
	air -c .air.toml

## build: Compile a production binary to ./bin/
build:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o $(BINARY) $(SERVER_PKG)
	@echo "Built $(BINARY)"

## test: Run all tests with race detection
test:
	go test ./... -race -count=1 -timeout 60s

## lint: Run golangci-lint (install separately: brew install golangci-lint)
lint:
	golangci-lint run ./...

## sqlc: Re-generate type-safe DB code from SQL queries
sqlc:
	sqlc generate

## swag: Re-generate OpenAPI spec and Swagger UI (run after changing handler annotations)
swag:
	swag init -g cmd/server/main.go -o docs --parseDependency --parseInternal

## tidy: Tidy and verify go modules
tidy:
	go mod tidy
	go mod verify

# ── Migrations ────────────────────────────────────────────────────────────────

## migrate-up: Apply all pending migrations
migrate-up:
	go run $(MIGRATE_PKG) up

## migrate-down: Roll back the last migration (pass N= to roll back N steps)
migrate-down:
	go run $(MIGRATE_PKG) down $(N)

## migrate-version: Print the current migration version
migrate-version:
	go run $(MIGRATE_PKG) version

## migrate-force V=<version>: Force-set migration version (recover from dirty state)
migrate-force:
	@test -n "$(V)" || (echo "usage: make migrate-force V=<version>" && exit 1)
	go run $(MIGRATE_PKG) force $(V)

## migrate-create name=<migration_name>: Scaffold a new up/down migration pair
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<migration_name>" && exit 1)
	$(eval VERSION := $(shell printf "%06d" $$(ls migrations/*.up.sql 2>/dev/null | wc -l | tr -d ' ' | awk '{print $$1+1}')))
	@touch migrations/$(VERSION)_$(name).up.sql migrations/$(VERSION)_$(name).down.sql
	@echo "Created:"
	@echo "  migrations/$(VERSION)_$(name).up.sql"
	@echo "  migrations/$(VERSION)_$(name).down.sql"

# ── Docker ────────────────────────────────────────────────────────────────────

## docker-up: Start postgres, redis, and the app with air (detached)
docker-up:
	docker compose up -d

## docker-down: Stop and remove all containers (preserves volumes)
docker-down:
	docker compose down

## docker-logs: Follow logs from all containers
docker-logs:
	docker compose logs -f

## docker-build: Rebuild the dev image (run after changing go.mod)
docker-build:
	docker compose build app

# ── Tooling ───────────────────────────────────────────────────────────────────

## install-tools: Install dev tools (air, sqlc, swag, golangci-lint)
install-tools:
	go install github.com/air-verse/air@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/swaggo/swag/cmd/swag@latest
	@echo "Install golangci-lint separately: brew install golangci-lint"

## clean: Remove build artefacts
clean:
	rm -rf bin/ tmp/ build-errors.log

# ── Help ──────────────────────────────────────────────────────────────────────

## help: List all available targets
help:
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/## /  /' | column -t -s ':'
	@echo ""
