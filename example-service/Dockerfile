# Build stage
FROM golang:1.24-alpine AS builder

# Install git for fetching dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy the parent module (snowflake-cache) for local dependency
COPY go.mod go.sum ../
COPY pkg/ ../pkg/

# Copy example-service files
COPY example-service/go.mod example-service/go.sum ./
COPY example-service/ ./

# Download dependencies
RUN go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /cache-compare .

# Final stage
FROM alpine:3.19

# Install ca-certificates for HTTPS connections to Snowflake/PostgreSQL
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /cache-compare .

# Run as non-root user
RUN adduser -D -g '' appuser
USER appuser

# No ports exposed - this is a background service

ENTRYPOINT ["./cache-compare"]

