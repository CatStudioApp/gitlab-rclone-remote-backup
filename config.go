package main

import (
	"flag"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the backup configuration
type Config struct {
	// Docker settings
	GitLabContainerName string
	RakeCommand         string

	// Backup settings
	BackupDir     string
	BackupPattern string // e.g., "*.tar" or "*_gitlab_backup.tar"
	MaxAge        time.Duration

	// Rclone settings
	RcloneRemotes []string // e.g., ["remote1:gitlab-backups", "remote2:backups/gitlab"]
	RcloneConfig  string   // path to rclone.conf

	// Optional features
	ZipPassword       string // if set, re-zip backup with password
	DiscordWebhookURL string // if set, send notifications

	// Scheduling
	CronSchedule string // if set, run on schedule (e.g., "0 3 * * *" for 3 AM daily)
	RunOnce      bool   // if true, run immediately and exit (ignoring schedule)

	// Retention
	NumBackupsToKeep int // number of backup files to keep on each remote (0 = disabled)
}

func parseFlags() Config {
	cfg := Config{}

	flag.StringVar(&cfg.GitLabContainerName, "container", getEnv("GITLAB_CONTAINER", "gitlab-web-1"), "GitLab container name or ID")
	flag.StringVar(&cfg.RakeCommand, "rake-cmd", getEnv("RAKE_COMMAND", "gitlab-rake gitlab:backup:create"), "Rake command to execute")
	flag.StringVar(&cfg.BackupDir, "backup-dir", getEnv("BACKUP_DIR", "/backups"), "Path to GitLab backup directory (mounted)")
	flag.StringVar(&cfg.BackupPattern, "pattern", getEnv("BACKUP_PATTERN", "*_gitlab_backup.tar"), "Backup file pattern")
	flag.StringVar(&cfg.RcloneConfig, "rclone-config", getEnv("RCLONE_CONFIG", "/config/rclone/rclone.conf"), "Path to rclone config file")

	maxAgeStr := getEnv("MAX_AGE", "1h")
	flag.DurationVar(&cfg.MaxAge, "max-age", mustParseDuration(maxAgeStr), "Maximum age for a valid backup")

	cfg.ZipPassword = getEnv("ZIP_PASSWORD", "")
	cfg.DiscordWebhookURL = getEnv("DISCORD_WEBHOOK_URL", "")
	cfg.CronSchedule = getEnv("CRON_SCHEDULE", "")
	cfg.NumBackupsToKeep = getEnvInt("NUM_OF_BACKUPS_TO_KEEP", 0)

	// New flag for manual trigger
	flag.BoolVar(&cfg.RunOnce, "now", false, "Run backup immediately and exit (overrides cron schedule)")

	remotesStr := getEnv("RCLONE_REMOTES", "")
	flag.Func("remotes", "Comma-separated list of rclone remotes (e.g., remote1:path,remote2:path)", func(s string) error {
		cfg.RcloneRemotes = parseRemotes(s)
		return nil
	})

	flag.Parse()

	// Parse remotes from env if not set via flag
	if len(cfg.RcloneRemotes) == 0 && remotesStr != "" {
		cfg.RcloneRemotes = parseRemotes(remotesStr)
	}

	if len(cfg.RcloneRemotes) == 0 {
		log.Fatal("At least one rclone remote is required. Set RCLONE_REMOTES env or use -remotes flag")
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("Warning: invalid integer for %s=%q, using default %d", key, v, defaultVal)
		return defaultVal
	}
	return i
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Fatalf("Invalid duration %q: %v", s, err)
	}
	return d
}

func parseRemotes(s string) []string {
	var remotes []string
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			remotes = append(remotes, r)
		}
	}
	return remotes
}
