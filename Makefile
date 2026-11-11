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

.PHONY: check fmt-check vet test build

check: fmt-check vet test build

fmt-check:
	@unformatted="$$($(GOFMT) -l .)" || exit 1; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

vet:
	$(GO) vet ./...

test:
	$(GO) test -race -count=1 ./...

build:
	$(GO) build -o bin/ ./cmd/...
