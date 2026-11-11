# Local checks for the Go code, in the order CI is expected to run them
# (F07 §10.1, L3). The docs toolchain under scripts/ is separate.

GO ?= go
# gofmt of the toolchain that builds the code, not whichever is first on PATH.
GOFMT := $(shell $(GO) env GOROOT)/bin/gofmt

# idp-bento-go resolves through its vanity module path and is not in the
# public checksum database.
export GOPRIVATE := go.idp.xyz
# A check must not rewrite go.mod or go.sum behind the caller's back.
export GOFLAGS := -mod=readonly

# The throwaway PostgreSQL of compose.yaml, as its two roles. The integration
# tests migrate as IDP_FABRIC_MIGRATOR_DSN and run as IDP_FABRIC_POSTGRES_DSN,
# the role a Fabric process connects with. Without them the tests are skipped,
# except in CI, where missing DSNs fail them.
LOCAL_MIGRATOR_DSN := postgres://fabric:fabric@127.0.0.1:55450/postgres?sslmode=disable
LOCAL_APPLICATION_DSN := postgres://fabric_app:fabric_app@127.0.0.1:55450/postgres?sslmode=disable

.PHONY: check fmt-check vet test build db-up db-down

check: fmt-check vet test build

fmt-check:
	@unformatted="$$($(GOFMT) -l .)" || exit 1; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

vet:
	$(GO) vet ./...

test:
	@[ -n "$$IDP_FABRIC_MIGRATOR_DSN$$IDP_FABRIC_POSTGRES_DSN" ] || echo "note: IDP_FABRIC_MIGRATOR_DSN and IDP_FABRIC_POSTGRES_DSN are not set, so the PostgreSQL tests are skipped (see make db-up)"
	$(GO) test -race -count=1 ./...

build:
	$(GO) build -o bin/ ./cmd/...

db-up:
	docker compose up -d --wait
	@echo 'export IDP_FABRIC_MIGRATOR_DSN="$(LOCAL_MIGRATOR_DSN)"'
	@echo 'export IDP_FABRIC_POSTGRES_DSN="$(LOCAL_APPLICATION_DSN)"'

db-down:
	docker compose down -v
