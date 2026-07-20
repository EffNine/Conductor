# Build stage
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata sqlite su-exec wget

RUN adduser -D -g '' novexa

WORKDIR /app

COPY --from=builder /app/gateway .
COPY deployments/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh \
 && mkdir -p /app/data \
 && chown -R novexa:novexa /app/data

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

# Entrypoint runs as root briefly to fix volume ownership, then drops to novexa.
ENTRYPOINT ["/docker-entrypoint.sh"]
