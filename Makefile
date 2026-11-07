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

.PHONY: check format vet lint test web-test web-build licenses cross bench check-postgres chaos canary real qualify service-test linux-bundle release-check snapshot install-test

check: format vet lint test web-test licenses release-check canary

# The list goes through a variable, because gofmt exits 0 when it finds a file to change.
format:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt would change:"; echo "$$unformatted"; exit 1; fi

# The mark the canary leaves stops a run after the tests that hid it.
canary:
	@test ! -e $(CANARY)/tripped || (echo "the claude canary ran" && rm $(CANARY)/tripped && exit 1)

vet:
	$(GO) vet ./...
	$(GO) vet -tags 'chaos postgres bench realsession service install' ./...

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

# The Linux check for a machine with no Go: the binaries, the doubles, and the compiled service, scope,
# pty, shim and pane kill, and bench tests for linux/amd64, with test/bundle/run.sh, which runs them
# from the bundle. BUNDLE names a directory that must not exist yet. The builds trim this machine's
# paths, so the bundle carries none of them.
BUNDLE_ARCH ?= amd64
BUNDLE_ENV := CGO_ENABLED=0 GOOS=linux GOARCH=$(BUNDLE_ARCH)

linux-bundle:
	@test -n "$(BUNDLE)" || (echo "set BUNDLE to the directory to write" && exit 1)
	@test ! -e "$(BUNDLE)" || (echo "$(BUNDLE) exists" && exit 1)
	mkdir -p "$(BUNDLE)/bin/canary" "$(BUNDLE)/tests"
	$(BUNDLE_ENV) $(GO) build -trimpath -o "$(BUNDLE)/bin/ropeshift" ./cmd/ropeshift
	$(BUNDLE_ENV) $(GO) build -trimpath -o "$(BUNDLE)/bin/ropeshiftd" ./cmd/ropeshiftd
	$(BUNDLE_ENV) $(GO) build -trimpath -tags chaos -o "$(BUNDLE)/bin/ropeshift-chaos" ./cmd/ropeshift
	$(BUNDLE_ENV) $(GO) build -trimpath -o "$(BUNDLE)/bin/fakeclaude" ./test/doubles/fakeclaude
	cp test/doubles/canary/claude "$(BUNDLE)/bin/canary/claude"
	$(BUNDLE_ENV) $(GO) test -c -trimpath -tags service -o "$(BUNDLE)/tests/service.test" ./test/service
	$(BUNDLE_ENV) $(GO) test -c -trimpath -o "$(BUNDLE)/tests/contract.test" ./test/contract
	$(BUNDLE_ENV) $(GO) test -c -trimpath -o "$(BUNDLE)/tests/localauth.test" ./internal/adapters/localauth
	$(BUNDLE_ENV) $(GO) test -c -trimpath -tags chaos -o "$(BUNDLE)/tests/chaos.test" ./test/chaos
	$(BUNDLE_ENV) $(GO) test -c -trimpath -tags bench -o "$(BUNDLE)/tests/bench.test" ./test/bench
	cp test/bundle/run.sh "$(BUNDLE)/run.sh"

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

# The release configuration, checked by GoReleaser when it is installed. A contributor without it
# skips the check, because only a maintainer cuts releases (docs/releasing.md).
release-check:
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser check; \
	else \
		echo "release-check: goreleaser is not installed, so the release configuration was not checked"; \
	fi
	@sh -n install.sh scripts/release.sh scripts/release-ui.sh scripts/homebrew-formula.sh

# A release as a tag would build it, from this commit with no tag and no publishing: the four
# archives with the web UI embedded, the .deb and .rpm packages, the checksums, the release notes, and
# the Homebrew formula, all in dist/. FORMULA_URL is where the formula fetches the archives, by default
# the release page of the snapshot's version.
FORMULA_URL ?=

snapshot:
	goreleaser release --snapshot --clean
	sh scripts/homebrew-formula.sh dist/checksums.txt "$(FORMULA_URL)" dist/homebrew/ropeshift.rb

# Installs the snapshot through every channel in a clean environment and asks each install its
# version: the install script on this machine and in Linux containers, the .deb and .rpm packages, the
# Homebrew formula, and go install at a release tag made in a scratch clone. It needs Docker, Node.js,
# and GoReleaser, and builds a fresh snapshot unless INSTALL_DIST names one.
INSTALL_DIST ?=

install-test:
	@if [ -z "$(INSTALL_DIST)" ]; then $(MAKE) --no-print-directory snapshot; fi
	ROPESHIFT_DIST="$(if $(INSTALL_DIST),$(abspath $(INSTALL_DIST)),$(CURDIR)/dist)" \
		$(GO) test -tags install -count=1 -v -timeout 30m ./test/install/...

# The release's qualification suite: the production probe on the installed CLI, which ends with that
# version's row for the matrix the binary ships (internal/adapters/claudecode/matrix.json).
qualify:
	@$(MAKE) --no-print-directory real SUITE=RS14
