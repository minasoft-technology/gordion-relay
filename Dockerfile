# Production Dockerfile for Gordion Relay
# Multi-stage build for optimized production image

# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go mod files first (for better caching)
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o relay \
    main.go

# Verify the binary
RUN ./relay --help

# Production stage
FROM scratch

# Import from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /build/relay /app/relay

# Create app directory structure
WORKDIR /app

# Set up non-root user
# Note: FROM scratch doesn't have /etc/passwd, so we skip USER directive

# Expose ports
EXPOSE 80/tcp 443/udp 8080/tcp

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/relay", "--help"] || exit 1

# Set environment variables for production
ENV TZ=UTC \
    GORDION_RELAY_LOG_FORMAT=json \
    GORDION_RELAY_LOG_LEVEL=info

# Run the relay server
CMD ["/app/relay", "-config", "/app/config.json"]

# Metadata
LABEL org.opencontainers.image.title="Gordion Relay" \
      org.opencontainers.image.description="QUIC-based reverse tunnel relay for hospital DICOM servers" \
      org.opencontainers.image.vendor="Minasoft Technology" \
      org.opencontainers.image.version="1.0.0" \
      org.opencontainers.image.licenses="Proprietary" \
      org.opencontainers.image.source="https://github.com/minasoft-technology/gordion-relay" \
      org.opencontainers.image.documentation="https://docs.zenpacs.com.tr/gordion-relay"