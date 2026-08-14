GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
CORE_BINARY ?= $(BINARY)
CODEX_BINARY ?= bin/zephyr-codex
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.dirty=$(DIRTY)

.PHONY: help build install install-cli uninstall uninstall-skill uninstall-cli update fmt fmt-check test test-golden test-evals vet validate-harnesses check

help:
	@echo "build               собрать $(CORE_BINARY) и $(CODEX_BINARY)"
	@echo "install             установить Zephyr CLI и пакет Codex"
	@echo "uninstall           удалить Zephyr CLI и пакет Codex"
	@echo "uninstall-skill     удалить пакет Zephyr из Codex"
	@echo "uninstall-cli       удалить только Zephyr CLI"
	@echo "update              обновить binary и пакет Codex"
	@echo "fmt                  отформатировать все Go-файлы"
	@echo "fmt-check            завершиться с ошибкой, если Go-файлам нужно форматирование"
	@echo "test                 запустить все Go-тесты"
	@echo "test-golden          запустить 12 детерминированных golden-fixtures"
	@echo "test-evals           проверить записи последующей оценки"
	@echo "vet                  запустить go vet"
	@echo "validate-harnesses   проверить ресурсы Codex"
	@echo "check                запустить форматирование, тесты, vet и проверку harness"

build:
	sh harnesses/build-cli-pair.sh "$(GO)" "$(LDFLAGS)" "$(CORE_BINARY)" "$(CODEX_BINARY)"

install: install-cli
	sh harnesses/install.sh

install-cli:
	@install_dir="$$(GOBIN="$${GOBIN:-}" "$(GO)" env GOBIN)"; \
	if [ -z "$$install_dir" ]; then install_dir="$$("$(GO)" env GOPATH | cut -d: -f1)/bin"; fi; \
	sh harnesses/build-cli-pair.sh "$(GO)" "$(LDFLAGS)" "$$install_dir/zephyr" "$$install_dir/zephyr-codex"

uninstall:
	$(MAKE) uninstall-skill
	$(MAKE) uninstall-cli

uninstall-skill:
	sh harnesses/uninstall.sh

uninstall-cli:
	GO="$(GO)" sh harnesses/uninstall-cli.sh

update:
	sh harnesses/update.sh --preflight
	$(MAKE) install-cli
	sh harnesses/update.sh

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
