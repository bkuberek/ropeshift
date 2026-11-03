# Layers of the test suite. `make check` runs at every commit and starts no
# agent session.

GO ?= go
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

# The PATH as it arrived, for the one layer that runs the real binary.
REAL_PATH := $(PATH)

# A `claude` that fails the run if anything executes it. The test layers put it first
# on PATH, and fakeclaude goes in front of it where a test needs an agent.
CANARY := $(CURDIR)/test/doubles/canary
export PATH := $(CANARY):$(PATH)

.PHONY: check format vet lint test web-test web-build licenses cross bench check-postgres chaos canary real qualify service-test

check: format vet lint test web-test licenses canary

# The list goes through a variable, because gofmt exits 0 when it finds a file to change.
format:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt would change:"; echo "$$unformatted"; exit 1; fi

# The mark the canary leaves stops a run after the tests that hid it.
canary:
	@test ! -e $(CANARY)/tripped || (echo "the claude canary ran" && rm $(CANARY)/tripped && exit 1)

vet:
	$(GO) vet ./...
	$(GO) vet -tags 'chaos postgres bench realsession service' ./...

lint:
	golangci-lint run ./...

# Always run: the exit tests build the daemon at run time, so the test cache cannot see
# a change to it and would repeat an old pass. The timeout sits past go test's default of
# 10 minutes, which the worker's package reaches on a busy machine with every test passing.
test:
	$(GO) test -race -count=1 -timeout 25m ./...

# The web application's types and tests, on the packages its lockfile pins.
web-test:
	@test -d web/node_modules || npm --prefix web ci
	npm --prefix web run typecheck
	npm --prefix web test

# Writes the application where the binary embeds it, so a build after this one serves it. A plain
# checkout serves the placeholder page, and a release commit adds this output on its tag (D28).
web-build:
	@test -d web/node_modules || npm --prefix web ci
	npm --prefix web run build

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

# The daemon under this machine's real service manager: launchd on macOS, and the user's systemd
# manager on Linux, which also runs the scope check. Each test installs a service under a name of its
# own, in a temporary installation, and removes it at its end.
service-test:
	$(GO) test -tags service -count=1 -v -timeout 15m ./test/service/...
	$(GO) test -count=1 -v -run '^TestAnOutlivingProcessLeavesTheControlGroup$$' ./test/contract/
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

# The one layer that starts real sessions, and only when asked by name. The guards'
# own tests run first, so a broken guard stops the run before it spends anything. A test
# timeout ends a run with no cleanup, which leaves its sessions behind, so the bound sits
# past anything a suite's own polls allow.
SUITE ?= RS1

real:
	$(GO) test -tags realsession -count=1 -run '^Test(Ledger|Refused|Environment|Canary|Stop|Init|Hook|Leftover)' ./test/real/...
	PATH="$(REAL_PATH)" ROPESHIFT_REAL_SESSIONS=1 $(GO) test -tags realsession -count=1 -timeout 20m -v -run '^Test$(SUITE)$$' ./test/real/...

# The release's qualification suite: the production probe on the installed CLI, which ends with that
# version's row for the matrix the binary ships (internal/adapters/claudecode/matrix.json).
qualify:
	@$(MAKE) --no-print-directory real SUITE=RS14
