# Build stage
FROM golang:1.21-alpine AS builder

# go-sqlite3 (via gorm) requires CGO + a C compiler.
RUN apk add --no-cache git ca-certificates gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" GOOS=linux \
    go build -ldflags="-s -w" -o /app/conductor ./cmd/conductor

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata sqlite su-exec wget libgcc

RUN adduser -D -g '' conductor

WORKDIR /app

COPY --from=builder /app/conductor .
COPY deployments/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh \
 && mkdir -p /app/data \
 && chown -R conductor:conductor /app/data

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

# Entrypoint runs as root briefly to fix volume ownership, then drops to conductor.
ENTRYPOINT ["/docker-entrypoint.sh"]
