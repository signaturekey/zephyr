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
GO_FILES := $(shell find cmd internal roles configs -type f -name '*.go' | sort)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.dirty=$(DIRTY)

.PHONY: help build install update uninstall fmt fmt-check test race vet check

help:
	@echo "build      build $(BINARY)"
	@echo "install    install Zephyr CLI with go install"
	@echo "update     install the latest tagged Zephyr release"
	@echo "uninstall  remove the installed Zephyr CLI"
	@echo "fmt        format Go files"
	@echo "fmt-check  fail when Go files need formatting"
	@echo "test       run unit and integration tests"
	@echo "race       run tests with the race detector"
	@echo "vet        run go vet"
	@echo "check      run fmt-check, test, race, and vet"

build:
	@mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BINARY)" ./cmd/zephyr

install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/zephyr

update:
	$(GO) install $(MODULE)/cmd/zephyr@$(UPDATE_VERSION)

uninstall:
	rm -f -- "$(INSTALLED_BINARY)"

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
