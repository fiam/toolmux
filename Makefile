GO ?= go
GOFLAGS ?=

.PHONY: build
build:
	$(GO) build $(GOFLAGS) -o bin/toolmux ./cmd/toolmux
	$(GO) build $(GOFLAGS) -o bin/auth-broker ./cmd/auth-broker

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
