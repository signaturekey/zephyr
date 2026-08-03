GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort)

.PHONY: help build install update update-codex update-claude update-all fmt fmt-check test test-golden test-evals vet validate-harnesses check

help:
	@echo "build               build $(BINARY)"
	@echo "install             install zephyr with go install"
	@echo "update              update binary + Codex harness"
	@echo "update-codex        update binary + Codex harness"
	@echo "update-claude       update binary + Claude harness"
	@echo "update-all          update binary + both harnesses"
	@echo "fmt                  format all Go files"
	@echo "fmt-check            fail if Go files need formatting"
	@echo "test                 run all Go tests"
	@echo "test-golden          run the 12 deterministic golden fixtures"
	@echo "test-evals           validate forward-evaluation records"
	@echo "vet                  run go vet"
	@echo "validate-harnesses   validate Codex and Claude harness assets"
	@echo "check                run formatting, tests, vet, and harness validation"

build:
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -o "$(BINARY)" ./cmd/zephyr

install:
	$(GO) install ./cmd/zephyr

update: update-codex

update-codex:
	$(MAKE) install
	sh harnesses/update.sh --codex

update-claude:
	$(MAKE) install
	sh harnesses/update.sh --claude

update-all:
	$(MAKE) install
	sh harnesses/update.sh --all

fmt:
	$(GOFMT) -w $(GO_FILES)

fmt-check:
	@test -z "$$($(GOFMT) -l $(GO_FILES))" || { \
		echo "Go files need formatting:"; \
		$(GOFMT) -l $(GO_FILES); \
		exit 1; \
	}

test:
	$(GO) test ./...

test-golden:
	$(GO) test ./fixtures/...

test-evals:
	$(GO) test ./evals/...

vet:
	$(GO) vet ./...

validate-harnesses:
	./harnesses/validate.sh

check: fmt-check test vet validate-harnesses
