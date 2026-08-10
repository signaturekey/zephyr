GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.dirty=$(DIRTY)

.PHONY: help build install install-cli install-codex install-claude install-opencode install-all install-skill-codex install-skill-claude install-skill-opencode install-skill-all uninstall uninstall-skill uninstall-cli update update-codex update-claude update-opencode update-all fmt fmt-check test test-golden test-evals vet validate-harnesses check

help:
	@echo "build               собрать $(BINARY)"
	@echo "install             установить только Zephyr CLI"
	@echo "install-codex       установить CLI и пакет harness Codex"
	@echo "install-claude      установить CLI и пакет harness Claude"
	@echo "install-opencode    установить CLI и пакет harness OpenCode"
	@echo "install-all         установить CLI и все пакеты harness"
	@echo "install-skill-*     установить один или все пакеты harness"
	@echo "uninstall           удалить Zephyr CLI и все пакеты harness"
	@echo "uninstall-skill     удалить пакеты Zephyr из всех harness"
	@echo "uninstall-cli       удалить только Zephyr CLI"
	@echo "update              обновить binary и harness Codex"
	@echo "update-codex        обновить binary и harness Codex"
	@echo "update-claude       обновить binary и harness Claude"
	@echo "update-opencode     обновить binary и harness OpenCode"
	@echo "update-all          обновить binary и все harness"
	@echo "fmt                  отформатировать все Go-файлы"
	@echo "fmt-check            завершиться с ошибкой, если Go-файлам нужно форматирование"
	@echo "test                 запустить все Go-тесты"
	@echo "test-golden          запустить 12 детерминированных golden-fixtures"
	@echo "test-evals           проверить записи последующей оценки"
	@echo "vet                  запустить go vet"
	@echo "validate-harnesses   проверить ресурсы harness Codex, Claude и OpenCode"
	@echo "check                запустить форматирование, тесты, vet и проверку harness"

build:
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/zephyr

install: install-cli

install-cli:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/zephyr

install-codex:
	$(MAKE) install-cli
	$(MAKE) install-skill-codex

install-claude:
	$(MAKE) install-cli
	$(MAKE) install-skill-claude

install-opencode:
	$(MAKE) install-cli
	$(MAKE) install-skill-opencode

install-all:
	$(MAKE) install-cli
	$(MAKE) install-skill-all

install-skill-codex:
	sh harnesses/install.sh --codex

install-skill-claude:
	sh harnesses/install.sh --claude

install-skill-opencode:
	sh harnesses/install.sh --opencode

install-skill-all:
	sh harnesses/install.sh --all

uninstall:
	$(MAKE) uninstall-skill
	$(MAKE) uninstall-cli

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

update-opencode:
	$(MAKE) install
	sh harnesses/update.sh --opencode

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
