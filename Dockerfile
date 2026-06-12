# Stage 1: Build
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with optimizations
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/walnut-billing \
    ./cmd/server

# Stage 2: Runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tini

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/walnut-billing /app/walnut-billing

# Create data directory for SQLite
RUN mkdir -p /app/data

# Expose port
EXPOSE 8082

# Use tini for proper signal handling
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["./walnut-billing"]
