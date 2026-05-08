GO ?= go
GOFLAGS ?=
DOCKER ?= docker
LINT_IMAGE ?= supacli-lint:dev
BIN_DIR ?= bin
CODESIGN_IDENTITY ?=
UNAME_S ?= $(shell uname -s)

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf 'Supacli development targets\n\n'
	@printf '  %-27s %s\n' 'make build' 'Build CLI and daemon binaries'
	@printf '  %-27s %s\n' 'make build-supaclid-image' 'Build generic supaclid OCI image'
	@printf '  %-27s %s\n' 'make dev-cli' 'Build CLI to ./bin and sign when configured'
	@printf '  %-27s %s\n' 'make fmt' 'Format Go source'
	@printf '  %-27s %s\n' 'make fmt-check' 'Check Go formatting'
	@printf '  %-27s %s\n' 'make lint' 'Run full Dockerfile-based linter pass'
	@printf '  %-27s %s\n' 'make commitlint' 'Check the latest commit message'
	@printf '  %-27s %s\n' 'make dev-server-tunnel' 'Run supaclid through Cloudflare Tunnel'
	@printf '  %-27s %s\n' 'make vet' 'Run go vet'
	@printf '  %-27s %s\n' 'make test' 'Run unit tests'
	@printf '  %-27s %s\n' 'make test-race' 'Run race tests'
	@printf '  %-27s %s\n' 'make test-integration' 'Run fake-upstream integration tests'
	@printf '  %-27s %s\n' 'make test-live' 'Run opt-in live-provider tests'
	@printf '  %-27s %s\n' 'make coverage' 'Write coverage.out'
	@printf '  %-27s %s\n' 'make clean' 'Remove generated artifacts'

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/supacli ./cmd/supacli
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/supaclid ./cmd/supaclid

.PHONY: dev-cli
dev-cli:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/supacli ./cmd/supacli
ifneq ($(strip $(CODESIGN_IDENTITY)),)
	codesign --force --sign "$(CODESIGN_IDENTITY)" --timestamp=none "$(BIN_DIR)/supacli"
else
ifeq ($(UNAME_S),Darwin)
	@echo "codesign skipped: CODESIGN_IDENTITY is not set"
endif
endif

.PHONY: build-supaclid-image
build-supaclid-image:
	$(DOCKER) build -t supaclid:dev .

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"

.PHONY: lint
lint:
	$(DOCKER) build --target lint -t $(LINT_IMAGE) .

.PHONY: commitlint
commitlint:
	@git log -1 --format=%B | scripts/check-commit-message.sh

.PHONY: dev-server-tunnel
dev-server-tunnel:
	scripts/dev-server-tunnel.sh

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: test-integration
test-integration:
	$(GO) test -run Integration ./...

.PHONY: test-live
test-live:
	@if [ "$$SUPACLI_LIVE_TESTS" != "1" ]; then echo "set SUPACLI_LIVE_TESTS=1 to run live tests"; exit 0; fi
	$(GO) test -run Live ./...

.PHONY: coverage
coverage:
	$(GO) test -coverprofile=coverage.out ./...

.PHONY: clean
clean:
	rm -rf bin dist coverage.out
