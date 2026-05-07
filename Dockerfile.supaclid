# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/supaclid \
    ./cmd/supaclid

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/supaclid /usr/local/bin/supaclid

EXPOSE 8080
ENTRYPOINT ["supaclid"]
CMD ["--addr", ":8080"]
