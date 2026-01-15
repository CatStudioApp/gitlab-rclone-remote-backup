# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY *.go ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gitlab-backup .

# Runtime stage
FROM alpine:3.21

# Install rclone and ca-certificates
RUN apk add --no-cache \
    rclone \
    ca-certificates \
    tzdata

# Copy the binary from builder
COPY --from=builder /gitlab-backup /usr/local/bin/gitlab-backup

# Create directories for mounts
RUN mkdir -p /backups /config/rclone

# Set default environment variables
ENV GITLAB_CONTAINER=gitlab-web-1 \
    BACKUP_DIR=/backups \
    RCLONE_CONFIG=/config/rclone/rclone.conf \
    BACKUP_PATTERN="*_gitlab_backup.tar" \
    MAX_AGE=1h

ENTRYPOINT ["/usr/local/bin/gitlab-backup"]
