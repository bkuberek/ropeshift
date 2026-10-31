# Layers of the test suite. `make check` runs at every commit and starts no
# agent session.

GO ?= go
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

# A `claude` that fails the run if anything executes it. The test layers put it first
# on PATH, and fakeclaude goes in front of it where a test needs an agent.
CANARY := $(CURDIR)/test/doubles/canary
export PATH := $(CANARY):$(PATH)

.PHONY: check vet lint test licenses cross bench check-postgres chaos canary

check: vet lint test licenses canary

# The mark the canary leaves stops a run after the tests that hid it.
canary:
	@test ! -e $(CANARY)/tripped || (echo "the claude canary ran" && rm $(CANARY)/tripped && exit 1)

vet:
	$(GO) vet ./...
	$(GO) vet -tags 'chaos postgres bench' ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test -race ./...

licenses:
	$(GO) run ./test/rules/licensereport

cross:
	@for target in $(TARGETS); do \
		echo "build $$target"; \
		CGO_ENABLED=0 GOOS=$${target%/*} GOARCH=$${target#*/} $(GO) build ./... || exit 1; \
	done

bench:
	$(GO) test -tags bench -count=1 -v ./test/bench/...
	@$(MAKE) --no-print-directory canary

# The contract suite on a private Postgres, which also prepares every statement
# on a real server, and the two defects only Postgres shows (D7). It needs initdb
# and postgres on PATH or in ROPESHIFT_TEST_PG_BIN.
check-postgres:
	$(GO) test -tags postgres -race -count=1 ./test/contract/...
	$(GO) test -tags 'chaos postgres' -count=1 -run TestRevisionLastFailsOnPostgres ./test/chaos/...

# Crash injection on real processes. KILLS sets the random kills per run, and
# KILLS=1500 gives the long run. The target refuses a live installation.
KILLS ?= 120

chaos:
	KILLS=$(KILLS) $(GO) test -tags chaos -count=1 -timeout 45m ./test/chaos/...
	@$(MAKE) --no-print-directory canary
