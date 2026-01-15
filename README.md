# GitLab Backup to Rclone

A Go application that automates GitLab backup and uploads to one or more rclone remotes.

## Features

- **Docker Integration**: Executes `gitlab-rake gitlab:backup:create` inside your GitLab container via Docker socket
- **Backup Verification**: Validates the latest backup exists and is recent enough
- **Multi-Remote Upload**: Uploads to one or more rclone destinations (S3, B2, GDrive, etc.)

## Quick Start

### 1. Configure rclone

Make sure you have rclone configured with your remote(s):

```bash
rclone config
# Add your remotes (b2, s3, gdrive, etc.)
```

### 2. Update docker-compose.yml

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock:ro
  - /srv/gitlab/data/backups:/backups:ro
  - ~/.config/rclone:/config/rclone:ro

environment:
  GITLAB_CONTAINER: gitlab-web-1
  RCLONE_REMOTES: "b2:gitlab-backups,gdrive:Backups/gitlab"
```

### 3. Run

```bash
# Build and run
docker-compose run --rm gitlab-backup

# Or build first
docker-compose build
docker-compose run --rm gitlab-backup
```

## Configuration

| Environment Variable | Flag | Default | Description |
|---------------------|------|---------|-------------|
| `GITLAB_CONTAINER` | `-container` | `gitlab-web-1` | GitLab container name/ID |
| `RAKE_COMMAND` | `-rake-cmd` | `gitlab-rake gitlab:backup:create` | Backup command |
| `BACKUP_DIR` | `-backup-dir` | `/backups` | Mounted backup directory |
| `BACKUP_PATTERN` | `-pattern` | `*_gitlab_backup.tar` | Glob pattern for backups |
| `MAX_AGE` | `-max-age` | `1h` | Max age for valid backup |
| `RCLONE_REMOTES` | `-remotes` | (required) | Comma-separated remotes |
| `RCLONE_CONFIG` | `-rclone-config` | `/config/rclone/rclone.conf` | Rclone config path |
| `ZIP_PASSWORD` | - | (optional) | Password to encrypt backup |
| `DISCORD_WEBHOOK_URL` | - | (optional) | Discord webhook for notifications |

## Required Mounts

1. **Docker Socket** (`/var/run/docker.sock`): Required to exec into GitLab container
2. **Backup Directory**: Where GitLab stores its backups (usually `/var/opt/gitlab/backups` or similar)
3. **Rclone Config**: Your rclone configuration file

## Scheduling

### Cron (Linux/macOS)

```bash
# Add to crontab: daily at 3 AM
0 3 * * * cd /path/to/gitlab-backup && docker-compose run --rm gitlab-backup >> /var/log/gitlab-backup.log 2>&1
```

### Kubernetes CronJob

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: gitlab-backup
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: backup
            image: your-registry/gitlab-backup:latest
            env:
            - name: RCLONE_REMOTES
              value: "s3:gitlab-backups"
            volumeMounts:
            - name: docker-sock
              mountPath: /var/run/docker.sock
            - name: backups
              mountPath: /backups
            - name: rclone-config
              mountPath: /config/rclone
          volumes:
          - name: docker-sock
            hostPath:
              path: /var/run/docker.sock
          - name: backups
            hostPath:
              path: /srv/gitlab/data/backups
          - name: rclone-config
            secret:
              secretName: rclone-config
          restartPolicy: OnFailure
```

## Development

```bash
# Run locally (requires Go 1.23+)
go run . -container gitlab-web-1 -remotes "local:/tmp/test-backup"

# Build
go build -o gitlab-backup .

# Test with dry run (no actual backup)
BACKUP_DIR=/path/to/existing/backups \
RCLONE_REMOTES="local:/tmp/backup-test" \
RAKE_COMMAND="echo 'dry run'" \
go run .
```

## License

MIT
