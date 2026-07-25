# gonow-dict — developer UI & manifest (Decision D10)
# Every meaningful action lives here; `make` alone shows this menu.
# New capabilities MUST land with a target.

BINARY     := gonow-dict
CMD        := ./cmd/gonow-dict
BUILD_DIR  := dist
# FTS5 is required by the ingest tier (mattn/go-sqlite3, arrives Phase 2);
# the tag is harmless before that and must never be dropped (D4).
GO_TAGS    := sqlite_fts5
GOFLAGS    := -tags $(GO_TAGS) -trimpath
LDFLAGS    := -s -w -X main.version=$(VERSION)
VERSION    := $(shell git -C . describe --tags --always --dirty 2>/dev/null || echo dev)

# Integration tests need real dictionaries; point these at files you have
# (tests skip silently when a path is unset/missing).
GONOW_TEST_MDX      ?= $(HOME)/Downloads/Language/mdict/es-es-Espasa-Calpe-2016.mdx
GONOW_TEST_STARDICT ?= $(HOME)/Downloads/Language/stardict/eng-eng-stanford-ep.ifo
GONOW_TEST_SLOB     ?= $(HOME)/Downloads/Language/aard/es-es-Espasa-Calpe-2016.slob
GONOW_TEST_DSL      ?= $(HOME)/Downloads/Language/DSL/es-es-Espasa-Calpe-2016/es-es-Espasa-Calpe-2016.dsl
TEST_ENV = GONOW_TEST_MDX="$(GONOW_TEST_MDX)" GONOW_TEST_STARDICT="$(GONOW_TEST_STARDICT)" GONOW_TEST_SLOB="$(GONOW_TEST_SLOB)" GONOW_TEST_DSL="$(GONOW_TEST_DSL)"

# Args for `make run-*` targets, e.g.: make run ARGS="list ~/Dictionaries"
ARGS ?=

.DEFAULT_GOAL := help

# ---- meta ---------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "gonow-dict make targets:"; echo
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z0-9_.-]+:.*##/{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "Vars: ARGS=<cli args>  GONOW_TEST_MDX|_STARDICT|_SLOB|_DSL=<integration fixtures>"

# ---- build & run --------------------------------------------------------

.PHONY: build
build: ## Build ./gonow-dict binary for the host platform
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: install
install: ## go install into GOBIN
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: run
run: build ## Build then run with ARGS, e.g. make run ARGS="lookup dict.mdx word"
	./$(BINARY) $(ARGS)

.PHONY: ingest
ingest: build ## Ingest DICT=<path> into a text.db (OUT=<path> optional), e.g. make ingest DICT=~/Dicts/x.mdx
	@test -n "$(DICT)" || { echo "usage: make ingest DICT=<dictfile> [OUT=<out.db>]"; exit 2; }
	./$(BINARY) ingest $(if $(OUT),-o "$(OUT)") "$(DICT)"

.PHONY: serve
serve: build ## Run the HTTP server (DICT_DIR/PORT/ARGS overridable), e.g. make serve DICT_DIR=~/Dicts
	./$(BINARY) serve $(if $(DICT_DIR),-dict-dir "$(DICT_DIR)") $(if $(PORT),-port $(PORT)) $(ARGS)

.PHONY: cross
cross: ## Cross-compile all release targets into dist/ (pure-Go sqlite via -tags purego, no C toolchain needed)
	@mkdir -p $(BUILD_DIR)
	@for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 linux/arm/7 linux/arm/6 windows/amd64 windows/arm64; do \
	  os=$$(echo $$target | cut -d/ -f1); arch=$$(echo $$target | cut -d/ -f2); arm=$$(echo $$target | cut -d/ -f3); \
	  ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  suffix="$$os-$$arch"; [ -n "$$arm" ] && suffix="$$suffix-v$$arm"; \
	  echo "building $$suffix"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$arm go build -trimpath -tags purego -ldflags "$(LDFLAGS)" \
	    -o $(BUILD_DIR)/$(BINARY)-$$suffix$$ext $(CMD) || exit 1; \
	done

.PHONY: test-purego
test-purego: ## Run store tests against the pure-Go sqlite driver (release parity)
	CGO_ENABLED=0 go test -tags purego ./internal/store/ ./internal/server/

# ---- quality ------------------------------------------------------------

.PHONY: test
test: ## Unit tests (integration tests skip unless GONOW_TEST_MDX exists)
	$(TEST_ENV) go test $(GOFLAGS) ./...

.PHONY: test-v
test-v: ## Tests, verbose
	$(TEST_ENV) go test $(GOFLAGS) -v ./...

.PHONY: cover
cover: ## Tests with coverage report
	$(TEST_ENV) go test $(GOFLAGS) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: bench
bench: ## Run benchmarks
	$(TEST_ENV) go test $(GOFLAGS) -bench=. -benchmem -run=^$$ ./...

.PHONY: vet
vet: ## go vet
	go vet $(GOFLAGS) ./...

.PHONY: fmt
fmt: ## gofmt all sources in place
	gofmt -w $$(find . -name '*.go' -not -path './dist/*')

.PHONY: lint
lint: vet ## golangci-lint if installed, else vet only
	@command -v golangci-lint >/dev/null && golangci-lint run --build-tags $(GO_TAGS) ./... || echo "golangci-lint not installed; ran vet only"

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: check
check: tidy vet test ## Pre-commit gate: tidy + vet + test

# ---- housekeeping -------------------------------------------------------

.PHONY: clean
clean: ## Remove binary, dist/, coverage artifacts
	rm -rf $(BINARY) $(BUILD_DIR) coverage.out

.PHONY: version
version: ## Print the version stamp used for builds
	@echo $(VERSION)
