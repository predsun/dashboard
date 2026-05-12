# dashboard Makefile — single-binary, no Node.js, no Docker.
#
# Bootstrap targets download the standalone Tailwind CSS CLI into .tools/
# so contributors do not need Node.js installed.

BINARY      := dashboard
PKG         := github.com/predsun/dashboard
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

GO ?= go

TAILWIND_VERSION := v3.4.13
TAILWIND_DIR     := .tools
TAILWIND_BIN     := $(TAILWIND_DIR)/tailwindcss

# Pinned versions of the vendored JS libraries. Bumping a version here is the
# entire upgrade procedure — there is no node_modules.
ALPINE_VERSION    := 3.14.1
HTMX_VERSION      := 2.0.3
SORTABLE_VERSION  := 1.15.3

JS_DIR := web/static/js

# Host platform detection for Tailwind download.
UNAME_S := $(shell uname -s 2>/dev/null || echo Windows)
UNAME_M := $(shell uname -m 2>/dev/null || echo x86_64)

ifeq ($(OS),Windows_NT)
	TAILWIND_HOST_OS := windows
	TAILWIND_BIN := $(TAILWIND_DIR)/tailwindcss.exe
else ifeq ($(UNAME_S),Darwin)
	TAILWIND_HOST_OS := macos
else
	TAILWIND_HOST_OS := linux
endif

ifeq ($(UNAME_M),x86_64)
	TAILWIND_HOST_ARCH := x64
else ifeq ($(UNAME_M),amd64)
	TAILWIND_HOST_ARCH := x64
else ifeq ($(UNAME_M),aarch64)
	TAILWIND_HOST_ARCH := arm64
else ifeq ($(UNAME_M),arm64)
	TAILWIND_HOST_ARCH := arm64
else
	TAILWIND_HOST_ARCH := x64
endif

ifeq ($(TAILWIND_HOST_OS),windows)
	TAILWIND_ASSET := tailwindcss-windows-$(TAILWIND_HOST_ARCH).exe
else
	TAILWIND_ASSET := tailwindcss-$(TAILWIND_HOST_OS)-$(TAILWIND_HOST_ARCH)
endif

TAILWIND_URL := https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$(TAILWIND_ASSET)

CSS_IN  := web/static/css/input.css
CSS_OUT := web/static/css/output.css

.PHONY: help
help:
	@echo "Targets:"
	@echo "  tailwind-cli   download standalone Tailwind into $(TAILWIND_DIR)"
	@echo "  tailwind       compile $(CSS_IN) -> $(CSS_OUT)"
	@echo "  tailwind-watch run Tailwind in watch mode"
	@echo "  build          build host binary into bin/$(BINARY)"
	@echo "  build-all      cross-compile linux/amd64, linux/arm64, darwin/arm64, windows/amd64"
	@echo "  run            tailwind + go run"
	@echo "  dev            run Tailwind --watch alongside the server"
	@echo "  test           go test ./..."
	@echo "  lint           go vet + staticcheck (if installed)"
	@echo "  release        build-all + SHA256SUMS into dist/"
	@echo "  clean          remove bin/, dist/, $(CSS_OUT)"

$(TAILWIND_BIN):
	@mkdir -p $(TAILWIND_DIR)
	@echo ">> downloading $(TAILWIND_ASSET)"
	@curl -fsSL -o $(TAILWIND_BIN) $(TAILWIND_URL)
	@chmod +x $(TAILWIND_BIN) 2>/dev/null || true

.PHONY: tailwind-cli
tailwind-cli: $(TAILWIND_BIN)

# Vendored JS libraries. We download once, commit nothing during build; the
# files live under web/static/js/ and are pulled into the binary via embed.
$(JS_DIR)/alpine.min.js:
	@mkdir -p $(JS_DIR)
	curl -fsSL -o $@ https://unpkg.com/alpinejs@$(ALPINE_VERSION)/dist/cdn.min.js

$(JS_DIR)/htmx.min.js:
	@mkdir -p $(JS_DIR)
	curl -fsSL -o $@ https://unpkg.com/htmx.org@$(HTMX_VERSION)/dist/htmx.min.js

$(JS_DIR)/sortable.min.js:
	@mkdir -p $(JS_DIR)
	curl -fsSL -o $@ https://cdn.jsdelivr.net/npm/sortablejs@$(SORTABLE_VERSION)/Sortable.min.js

.PHONY: vendor-js
vendor-js: $(JS_DIR)/alpine.min.js $(JS_DIR)/htmx.min.js $(JS_DIR)/sortable.min.js

.PHONY: tailwind
tailwind: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(CSS_IN) -o $(CSS_OUT) --minify

.PHONY: tailwind-watch
tailwind-watch: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(CSS_IN) -o $(CSS_OUT) --watch

.PHONY: build
build: tailwind vendor-js
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/dashboard

.PHONY: build-all
build-all: tailwind vendor-js
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64   ./cmd/dashboard
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64   ./cmd/dashboard
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64  ./cmd/dashboard
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/dashboard

.PHONY: run
run: tailwind
	$(GO) run ./cmd/dashboard

.PHONY: dev
dev: $(TAILWIND_BIN)
	@echo ">> starting Tailwind --watch and go run in parallel"
	@( $(TAILWIND_BIN) -i $(CSS_IN) -o $(CSS_OUT) --watch & echo $$! > .tailwind.pid; $(GO) run ./cmd/dashboard; kill $$(cat .tailwind.pid) 2>/dev/null; rm -f .tailwind.pid )

.PHONY: test
test:
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint:
	$(GO) vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "(staticcheck not installed, skipped)"

.PHONY: release
release: build-all
	cd dist && sha256sum $(BINARY)-* > SHA256SUMS

.PHONY: clean
clean:
	rm -rf bin dist $(CSS_OUT) .tailwind.pid
