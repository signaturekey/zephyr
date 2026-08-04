GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort)

.PHONY: help build install install-cli install-codex install-claude install-all install-skill-codex install-skill-claude install-skill-all uninstall uninstall-skill uninstall-cli update update-codex update-claude update-all fmt fmt-check test test-golden test-evals vet validate-harnesses check

help:
	@echo "build               build $(BINARY)"
	@echo "install             install only the Zephyr CLI"
	@echo "install-codex       install CLI + Codex harness package"
	@echo "install-claude      install CLI + Claude Code harness package"
	@echo "install-all         install CLI + all harness packages"
	@echo "install-skill-*     install only one or all harness packages"
	@echo "uninstall           remove the installed Zephyr CLI and all harness packages"
	@echo "uninstall-skill     remove Zephyr packages from all harnesses"
	@echo "uninstall-cli       remove only the installed Zephyr CLI"
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

install: install-cli

install-cli:
	$(GO) install ./cmd/zephyr

install-codex:
	$(MAKE) install-cli
	$(MAKE) install-skill-codex

install-claude:
	$(MAKE) install-cli
	$(MAKE) install-skill-claude

install-all:
	$(MAKE) install-cli
	$(MAKE) install-skill-all

install-skill-codex:
	sh harnesses/install.sh --codex

install-skill-claude:
	sh harnesses/install.sh --claude

install-skill-all:
	sh harnesses/install.sh --all

uninstall:
	sh harnesses/uninstall.sh --all
	GO="$(GO)" sh harnesses/uninstall-cli.sh

uninstall-skill:
	sh harnesses/uninstall.sh --all

uninstall-cli:
	GO="$(GO)" sh harnesses/uninstall-cli.sh

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
