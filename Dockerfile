# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.3
ARG ALPINE_VERSION=3.23

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS go-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM go-base AS lint-tools
ARG ACTIONLINT_VERSION=v1.7.12
ARG GITLEAKS_VERSION=v8.30.1
ARG GOLANGCI_LINT_VERSION=v2.12.2
ARG GOSEC_VERSION=v2.26.1
ARG GOVULNCHECK_VERSION=v1.3.0
ARG STATICCHECK_VERSION=v0.7.0

RUN apk add --no-cache git yamllint
RUN go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION} && \
    go install github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION} && \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} && \
    go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} && \
    go install github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION} && \
    go install github.com/gitleaks/gitleaks/v8@${GITLEAKS_VERSION}

FROM lint-tools AS lint
COPY . .

RUN test -z "$(gofmt -l $(find . -name '*.go' -not -path './.git/*'))"
RUN actionlint
RUN find . -name '*.yaml' -not -path './.git/*' -print0 | \
    xargs -0 yamllint -c .yamllint.yaml
RUN go vet ./...
RUN staticcheck ./...
RUN golangci-lint run --config .golangci.yaml ./...
RUN govulncheck ./...
RUN gosec ./...
RUN gitleaks detect --no-git --source .

FROM go-base AS build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/supaclid \
    ./cmd/supaclid

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates
COPY --from=build /out/supaclid /usr/local/bin/supaclid

EXPOSE 8080
ENTRYPOINT ["supaclid"]
CMD ["--addr", ":8080"]
