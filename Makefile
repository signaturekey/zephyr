GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
MODULE_PATH := github.com/signaturekey/zephyr
TAG ?=
ZEPHYR_INSTALL_TAG := $(strip $(TAG))
export ZEPHYR_INSTALL_TAG
GO_BIN := $(shell $(GO) env GOBIN)
ifeq ($(strip $(GO_BIN)),)
GO_BIN := $(shell $(GO) env GOPATH)/bin
endif
INSTALLED_BINARY := $(GO_BIN)/zephyr
HARNESS_SKILLS_DIR ?= $(HOME)/.agents/skills
HARNESS_SKILL_SOURCE := .agents/skills/zephyr
HARNESS_SKILL_TARGET := $(HARNESS_SKILLS_DIR)/zephyr
CODEX_RESTART_MESSAGE := Перезапустите Codex, чтобы применить изменения Zephyr.
GO_FILES := $(shell find cmd internal roles configs -type f -name '*.go' | sort)
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.dirty=$(DIRTY)

.PHONY: help build install install-tag install-cli install-skill update uninstall uninstall-cli uninstall-skill fmt fmt-check test race vet check

help:
	@echo "build      собрать $(BINARY)"
	@echo "install    установить CLI и skill из checkout или TAG=vX.Y.Z"
	@echo "update     alias на install"
	@echo "uninstall  удалить Zephyr CLI и пользовательский skill"
	@echo "fmt        отформатировать Go-файлы"
	@echo "fmt-check  проверить форматирование Go-файлов"
	@echo "test       запустить unit- и integration-тесты"
	@echo "race       запустить тесты с race detector"
	@echo "vet        запустить go vet"
	@echo "check      выполнить fmt-check, test, race и vet"

build:
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/zephyr

ifeq ($(ZEPHYR_INSTALL_TAG),)
install: install-cli install-skill
else
install: install-tag
endif

install:
	@echo "$(CODEX_RESTART_MESSAGE)"

install-tag:
	@set -eu; \
		tag="$$ZEPHYR_INSTALL_TAG"; \
		if ! printf '%s\n' "$$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+incompatible)?$$'; then \
			echo "TAG must be a semantic version such as v0.1.0 or v0.2.0-rc.1" >&2; \
			exit 2; \
		fi; \
		module='$(MODULE_PATH)'; \
		$(GO) mod download "$$module@$$tag"; \
		module_dir="$$( $(GO) list -m -f '{{.Dir}}' "$$module@$$tag" )"; \
		commit="$$( $(GO) list -m -f '{{with .Origin}}{{.Hash}}{{end}}' "$$module@$$tag" )"; \
		if printf '%s\n' "$$commit" | grep -Eq '^[0-9a-fA-F]{40}$$'; then \
			commit="$$(printf '%s' "$$commit" | cut -c1-12)"; \
		else \
			commit=unknown; \
		fi; \
		skill_source="$$module_dir/.agents/skills/zephyr"; \
		if test -z "$$module_dir" || ! test -f "$$skill_source/SKILL.md"; then \
			echo "Zephyr skill is missing from $$module@$$tag" >&2; \
			exit 1; \
		fi; \
		mkdir -p "$(HARNESS_SKILLS_DIR)"; \
		stage="$$(mktemp -d "$(HARNESS_SKILLS_DIR)/.zephyr-install.XXXXXX")"; \
		trap 'rm -rf -- "$$stage"' EXIT HUP INT TERM; \
		cp -R "$$skill_source/." "$$stage/"; \
		chmod -R u+w "$$stage"; \
		$(GO) install -ldflags "-X main.version=$$tag -X main.commit=$$commit -X main.dirty=false" "$$module/cmd/zephyr@$$tag"; \
		rm -rf -- "$(HARNESS_SKILL_TARGET)"; \
		mv "$$stage" "$(HARNESS_SKILL_TARGET)"

install-cli:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/zephyr

install-skill:
	test -f "$(HARNESS_SKILL_SOURCE)/SKILL.md"
	rm -rf -- "$(HARNESS_SKILL_TARGET)"
	mkdir -p "$(HARNESS_SKILL_TARGET)"
	cp -R "$(HARNESS_SKILL_SOURCE)/." "$(HARNESS_SKILL_TARGET)/"

update: install

uninstall: uninstall-cli uninstall-skill
	@echo "$(CODEX_RESTART_MESSAGE)"

uninstall-cli:
	rm -f -- "$(INSTALLED_BINARY)"

uninstall-skill:
	rm -rf -- "$(HARNESS_SKILL_TARGET)"

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

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: fmt-check test race vet
