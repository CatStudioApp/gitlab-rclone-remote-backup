package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
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
}

func main() {
	cfg := parseFlags()

	log.Println("=== GitLab Backup Tool ===")
	log.Printf("Container: %s", cfg.GitLabContainerName)
	log.Printf("Backup Dir: %s", cfg.BackupDir)
	log.Printf("Rclone Remotes: %v", cfg.RcloneRemotes)

	ctx := context.Background()

	// Step 1: Create GitLab backup via Docker exec
	if err := createGitLabBackup(ctx, cfg); err != nil {
		log.Fatalf("Failed to create GitLab backup: %v", err)
	}

	// Step 2: Find and verify the latest backup
	backupFile, err := findLatestBackup(cfg)
	if err != nil {
		log.Fatalf("Failed to find latest backup: %v", err)
	}
	log.Printf("Latest backup found: %s", backupFile)

	// Step 3: Upload to rclone remotes
	if err := uploadToRemotes(cfg, backupFile); err != nil {
		log.Fatalf("Failed to upload backup: %v", err)
	}

	log.Println("=== Backup completed successfully ===")
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

// createGitLabBackup executes the gitlab-rake backup command inside the GitLab container
func createGitLabBackup(ctx context.Context, cfg Config) error {
	log.Println("Step 1: Creating GitLab backup...")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	// Create exec configuration
	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", cfg.RakeCommand},
		AttachStdout: true,
		AttachStderr: true,
	}

	// Create exec instance
	execResp, err := cli.ContainerExecCreate(ctx, cfg.GitLabContainerName, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to exec instance
	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()

	// Read output
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check exec exit code
	inspectResp, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		log.Printf("STDOUT:\n%s", stdout.String())
		log.Printf("STDERR:\n%s", stderr.String())
		return fmt.Errorf("backup command exited with code %d", inspectResp.ExitCode)
	}

	log.Println("GitLab backup command completed successfully")
	if stdout.Len() > 0 {
		// Print last few lines of output
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		for _, line := range lines {
			log.Printf("  > %s", line)
		}
	}

	return nil
}

// findLatestBackup finds the most recent backup file in the backup directory
func findLatestBackup(cfg Config) (string, error) {
	log.Println("Step 2: Finding latest backup...")

	pattern := filepath.Join(cfg.BackupDir, cfg.BackupPattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to glob pattern %q: %w", pattern, err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no backup files found matching pattern %q", pattern)
	}

	// Sort by modification time (newest first)
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var files []fileInfo

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			log.Printf("Warning: cannot stat %s: %v", match, err)
			continue
		}
		files = append(files, fileInfo{path: match, modTime: info.ModTime()})
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no accessible backup files found")
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	latest := files[0]

	// Verify the backup is recent enough
	age := time.Since(latest.modTime)
	if age > cfg.MaxAge {
		return "", fmt.Errorf("latest backup %q is too old (age: %v, max: %v)", latest.path, age, cfg.MaxAge)
	}

	info, _ := os.Stat(latest.path)
	log.Printf("Backup verified: %s (size: %d bytes, age: %v)", filepath.Base(latest.path), info.Size(), age.Round(time.Second))

	return latest.path, nil
}

// uploadToRemotes uploads the backup file to all configured rclone remotes
func uploadToRemotes(cfg Config, backupFile string) error {
	log.Println("Step 3: Uploading to rclone remotes...")

	backupName := filepath.Base(backupFile)
	var lastErr error

	for i, remote := range cfg.RcloneRemotes {
		log.Printf("  [%d/%d] Uploading to %s...", i+1, len(cfg.RcloneRemotes), remote)

		dest := fmt.Sprintf("%s/%s", strings.TrimSuffix(remote, "/"), backupName)

		args := []string{
			"--config", cfg.RcloneConfig,
			"copyto",
			backupFile,
			dest,
			"--progress",
			"--stats-one-line",
		}

		cmd := exec.Command("rclone", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			log.Printf("  ERROR: Failed to upload to %s: %v", remote, err)
			lastErr = err
			continue
		}

		log.Printf("  OK: Uploaded to %s", remote)
	}

	if lastErr != nil {
		return fmt.Errorf("one or more uploads failed (last error: %w)", lastErr)
	}

	return nil
}

// streamOutput streams the exec output to stdout/stderr (for real-time visibility)
func streamOutput(reader io.Reader, prefix string) {
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				if line != "" {
					log.Printf("%s: %s", prefix, line)
				}
			}
		}
		if err != nil {
			break
		}
	}
}
