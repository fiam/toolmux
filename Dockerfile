# syntax=docker/dockerfile:1

FROM golang:1.26.3-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/toolmuxd \
    ./cmd/toolmuxd

FROM alpine:3.22

RUN apk add --no-cache ca-certificates
COPY --from=build /out/toolmuxd /usr/local/bin/toolmuxd

EXPOSE 8080
ENTRYPOINT ["toolmuxd"]
CMD ["--addr", ":8080"]
