GO ?= go
GOFLAGS ?=

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf 'Toolmux development targets\n\n'
	@printf '  %-27s %s\n' 'make build' 'Build CLI and daemon binaries'
	@printf '  %-27s %s\n' 'make build-toolmuxd-image' 'Build generic toolmuxd OCI image'
	@printf '  %-27s %s\n' 'make fmt' 'Format Go source'
	@printf '  %-27s %s\n' 'make fmt-check' 'Check Go formatting'
	@printf '  %-27s %s\n' 'make lint' 'Run local linters when installed'
	@printf '  %-27s %s\n' 'make commitlint' 'Check the latest commit message'
	@printf '  %-27s %s\n' 'make dev-server-tunnel' 'Run toolmuxd through cloudflared'
	@printf '  %-27s %s\n' 'make vet' 'Run go vet'
	@printf '  %-27s %s\n' 'make test' 'Run unit tests'
	@printf '  %-27s %s\n' 'make test-race' 'Run race tests'
	@printf '  %-27s %s\n' 'make test-integration' 'Run fake-upstream integration tests'
	@printf '  %-27s %s\n' 'make test-live' 'Run opt-in live-provider tests'
	@printf '  %-27s %s\n' 'make coverage' 'Write coverage.out'
	@printf '  %-27s %s\n' 'make clean' 'Remove generated artifacts'

.PHONY: build
build:
	$(GO) build $(GOFLAGS) -o bin/toolmux ./cmd/toolmux
	$(GO) build $(GOFLAGS) -o bin/toolmuxd ./cmd/toolmuxd

.PHONY: build-toolmuxd-image
build-toolmuxd-image:
	docker build -f Dockerfile.toolmuxd -t toolmuxd:dev .

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"

.PHONY: lint
lint: fmt-check vet
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck not installed"; fi
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed"; fi
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; else echo "govulncheck not installed"; fi
	@if command -v gosec >/dev/null 2>&1; then gosec ./...; else echo "gosec not installed"; fi
	@if command -v gitleaks >/dev/null 2>&1; then gitleaks detect --no-git; else echo "gitleaks not installed"; fi

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
	@if [ "$$TOOLMUX_LIVE_TESTS" != "1" ]; then echo "set TOOLMUX_LIVE_TESTS=1 to run live tests"; exit 0; fi
	$(GO) test -run Live ./...

.PHONY: coverage
coverage:
	$(GO) test -coverprofile=coverage.out ./...

.PHONY: clean
clean:
	rm -rf bin dist coverage.out
