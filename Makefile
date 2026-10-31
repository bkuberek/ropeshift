# Layers of the test suite. `make check` runs at every commit and starts no
# agent session.

GO ?= go
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: check vet lint test licenses cross bench

check: vet lint test licenses

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
