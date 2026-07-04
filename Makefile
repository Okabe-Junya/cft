GO          ?= go
BIN_DIR     := bin
BIN         := $(BIN_DIR)/cft
PKG         := ./...
# Version comes from Go 1.24+ VCS stamping; no -X injection needed.
LDFLAGS     := -s -w
# Keep this pin in sync with the `version:` input of golangci-lint-action
# in .github/workflows/lint.yml.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

.PHONY: all build test integration lint check tidy clean

all: check build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/cft

test:
	$(GO) test -race -shuffle=on $(PKG)

# Exercises the real macOS Keychain. Skipped by default; macOS only.
# Uses throwaway keychain files under a temp dir; the login keychain is
# never touched and no prompts appear.
integration:
	$(GO) test -tags=integration ./internal/keychain/...

lint:
	$(GOLANGCI_LINT) run

check: lint test

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)
