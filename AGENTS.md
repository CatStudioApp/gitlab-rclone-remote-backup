# Project Context: GitLab Rclone Backup

This project is a specialized utility to backup a self-hosted GitLab instance and offload the backup to remote storage using [rclone](https://rclone.org).

## Architecture

1.  **Language**: Go (Go 1.23+)
2.  **Runtime**: Docker (Alpine-based)
3.  **Core Logic**: `main.go`
    *   Connects to Host Docker Daemon via `/var/run/docker.sock`.
    *   Executes `gitlab-rake gitlab:backup:create` inside the target GitLab container.
    *   Verifies the backup creation (checks timestamp/size).
    *   (Optional) Zips the directory with a password.
    *   Invokes `rclone` (installed in image) to upload to configured remotes.
    *   (Optional) Sends webhook notifications to Discord.

## Key Files

*   `main.go`: Single-file entry point containing all logic.
*   `Dockerfile`: Multi-stage build.
    *   Stage 1: Golang builder.
    *   Stage 2: Alpine runtime with `rclone`, `zip`, `ca-certificates`.
*   `.github/workflows/docker.yml`: Builds multi-arch (amd64/arm64) images to GHCR.

## Environment Configuration

The application is configured entirely via Environment Variables:

| Variable | Purpose |
| :--- | :--- |
| `GITLAB_CONTAINER` | Name of the container to exec into (e.g., `gitlab-web-1`). |
| `BACKUP_DIR` | Path where GitLab dumps backups (mounted volume). |
| `RCLONE_REMOTES` | Comma-separated list of rclone destinations (e.g., `s3:bucket`). |
| `RCLONE_CONFIG` | Path to `rclone.conf` (mounted volume). |
| `ZIP_PASSWORD` | (Optional) If set, encrypts backup into a `.zip` before upload. |
| `DISCORD_WEBHOOK_URL` | (Optional) Monitoring/Alerting webhook. |

## Development Rules

1.  **Docker Compatibility**: The tool relies on the Docker Socket. When testing locally, ensure you have access to a Docker daemon or mock the interactions.
2.  **Rclone Dependency**: The runtime image MUST have `rclone` installed. `main.go` calls the `rclone` binary via `os/exec`.
3.  **Go Version**: Keep `go.mod` aligned with the Dockerfile (currently `1.23`).
4.  **Linting**: Run `go mod tidy` and formatting before commits.

## Common Operations

*   **Build Local**: `go build -o gitlab-backup .`
*   **Build Docker**: `docker build -t gitlab-backup .`
*   **Run**: `docker-compose run --rm gitlab-backup`
