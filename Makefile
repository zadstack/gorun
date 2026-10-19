VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test run lint e2e css css-check loadtest

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/gorun ./cmd/gorun

test:
	go test ./...

run:
	go run ./cmd/gorun serve

# css rebuilds themes/default/assets/tailwind.css with the pinned Tailwind
# standalone CLI (downloaded once, checksum verified; no Node). Commit the
# result. css-check fails when the committed file is stale (CI runs it).
css:
	tools/tailwind/build.sh

css-check:
	tools/tailwind/build.sh --check

# staticcheck built against an older toolchain cannot read go1.27.2 export
# data. lint retries once with LINT_TOOLCHAIN when (and only when) that error
# appears, then skips with a message if it persists; real findings still fail.
LINT_TOOLCHAIN ?= go1.27.1
EXPORT_DATA_ERR := export data|unsupported version|could not import

lint:
	go vet ./...
	@if ! command -v staticcheck >/dev/null 2>&1; then \
		echo "lint: staticcheck not installed, skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
		exit 0; \
	fi; \
	out=$$(staticcheck ./... 2>&1); status=$$?; \
	if [ $$status -ne 0 ] && printf '%s\n' "$$out" | grep -qE '$(EXPORT_DATA_ERR)'; then \
		echo "lint: staticcheck cannot read this toolchain's export data, retrying with GOTOOLCHAIN=$(LINT_TOOLCHAIN)"; \
		out=$$(GOTOOLCHAIN=$(LINT_TOOLCHAIN) staticcheck ./... 2>&1); status=$$?; \
		if [ $$status -ne 0 ] && printf '%s\n' "$$out" | grep -qE '$(EXPORT_DATA_ERR)|toolchain'; then \
			printf '%s\n' "$$out" | head -5; \
			echo "lint: staticcheck skipped (export-data/toolchain issue, not a lint finding)"; \
			exit 0; \
		fi; \
	fi; \
	if [ -n "$$out" ]; then printf '%s\n' "$$out"; fi; \
	exit $$status

# e2e runs the Playwright browser suite (node/npm live only in e2e/; the Go
# app has no node deps). It builds the binary, boots it on a free port with a
# temp data dir. Admin/default-theme scripts are local; custom themes may use CDNs.
# The default theme's CSS is precompiled (make css), so the app needs no Node.
# See docs/E2E.md.
e2e:
	cd e2e && npm ci && npx playwright install chromium && npx playwright test

# loadtest boots a local SQLite gorun serve and reports p50/p95 latency, error
# rate, throughput, and approximate RSS/CPU. Not part of CI (host-noisy).
# See docs/LOADTEST.md. Override: LOADTEST_FLAGS='-concurrency 50 -duration 20s'
LOADTEST_FLAGS ?=
loadtest:
	go run ./tools/loadtest $(LOADTEST_FLAGS)
