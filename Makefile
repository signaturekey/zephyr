GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/zephyr
MODULE ?= github.com/signaturekey/zephyr
UPDATE_VERSION ?= latest
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
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.dirty=$(DIRTY)

.PHONY: help build install install-cli install-skill update uninstall uninstall-cli uninstall-skill fmt fmt-check test race vet check

help:
	@echo "build      build $(BINARY)"
	@echo "install    install Zephyr CLI and user harness skill"
	@echo "update     install the latest tagged Zephyr release"
	@echo "uninstall  remove Zephyr CLI and user harness skill"
	@echo "fmt        format Go files"
	@echo "fmt-check  fail when Go files need formatting"
	@echo "test       run unit and integration tests"
	@echo "race       run tests with the race detector"
	@echo "vet        run go vet"
	@echo "check      run fmt-check, test, race, and vet"

build:
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/zephyr

install: install-cli install-skill
	@echo "$(CODEX_RESTART_MESSAGE)"

install-cli:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/zephyr

install-skill:
	test -f "$(HARNESS_SKILL_SOURCE)/SKILL.md"
	rm -rf -- "$(HARNESS_SKILL_TARGET)"
	mkdir -p "$(HARNESS_SKILL_TARGET)"
	cp -R "$(HARNESS_SKILL_SOURCE)/." "$(HARNESS_SKILL_TARGET)/"

update:
	$(GO) install $(MODULE)/cmd/zephyr@$(UPDATE_VERSION)
	@echo "$(CODEX_RESTART_MESSAGE)"

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
