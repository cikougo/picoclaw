# ============================================================
# Stage 1: Build the picoclaw binary
# ============================================================
FROM golang:1.26.0-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN make build

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata curl

# Copy binary
COPY --from=builder /src/build/picoclaw /usr/local/bin/picoclaw

# Create non-root user and data directory
RUN addgroup -g 1000 picoclaw && \
    adduser -D -u 1000 -G picoclaw picoclaw && \
    mkdir -p /data/.picoclaw && \
    chown -R picoclaw:picoclaw /data

# Default environment for Railway/cloud deployment
ENV HOME=/data \
    ADMIN_USERNAME=admin \
    PICOCLAW_GATEWAY_HOST=0.0.0.0 \
    PICOCLAW_GATEWAY_PORT=18790 \
    PICOCLAW_AGENTS_DEFAULTS_WORKSPACE=/data/.picoclaw/workspace

EXPOSE 8080

# Switch to non-root user
USER picoclaw

# Copy startup script
COPY --chown=picoclaw:picoclaw start.sh /usr/local/bin/start.sh

# Health check (works for both gateway and dashboard modes)
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q --spider http://localhost:${PORT:-18790}/health || exit 1

CMD ["/usr/local/bin/start.sh"]
