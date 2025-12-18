# Build stage
# NOTE: This Dockerfile expects to be built with the build context set to the
# parent directory containing both snowflake-cache/ and db-cache/ directories.
# Example: docker build -f snowflake-cache/example-service/Dockerfile .
FROM golang:1.23-alpine AS builder

# Install git for fetching dependencies
RUN apk add --no-cache git ca-certificates

# Set up directory structure to match go.mod replace directives:
# - /build/snowflake-cache/example-service (this service)
# - /build/snowflake-cache (parent module) - replace: ../
# - /build/db-cache (external dependency) - replace: ../../db-cache
WORKDIR /build

# Copy the parent module (snowflake-cache)
COPY snowflake-cache/go.mod snowflake-cache/go.sum ./snowflake-cache/
COPY snowflake-cache/pkg/ ./snowflake-cache/pkg/

# Copy db-cache dependency
COPY db-cache/ ./db-cache/

# Copy example-service files
COPY snowflake-cache/example-service/ ./snowflake-cache/example-service/

WORKDIR /build/snowflake-cache/example-service

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

