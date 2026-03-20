package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/yeka/zip"
)

// runBackup executes the backup workflow once
func runBackup(cfg Config) error {
	var backupFile string
	var uploadFile string
	startTime := time.Now()

	ctx := context.Background()

	// Snapshot existing backups before creating a new one
	beforeFiles, err := listBackupFiles(cfg.BackupDir, cfg.BackupPattern)
	if err != nil {
		err = fmt.Errorf("failed to snapshot backup directory: %w", err)
		sendDiscordNotification(cfg, false, err.Error(), backupFile, time.Since(startTime))
		return err
	}

	// Step 1: Create GitLab backup via Docker exec
	if err := createGitLabBackup(ctx, cfg); err != nil {
		err = fmt.Errorf("failed to create GitLab backup: %w", err)
		sendDiscordNotification(cfg, false, err.Error(), backupFile, time.Since(startTime))
		return err
	}

	// Step 2: Find and verify the latest backup
	backupFile, err = findLatestBackup(cfg, beforeFiles)
	if err != nil {
		err = fmt.Errorf("failed to find latest backup: %w", err)
		sendDiscordNotification(cfg, false, err.Error(), backupFile, time.Since(startTime))
		return err
	}
	log.Printf("Latest backup found: %s", backupFile)

	// Step 2.5: Optional password-protected zip
	uploadFile = backupFile
	if cfg.ZipPassword != "" {
		uploadFile, err = createPasswordZip(backupFile, cfg.ZipPassword)
		if err != nil {
			err = fmt.Errorf("failed to create password zip: %w", err)
			sendDiscordNotification(cfg, false, err.Error(), backupFile, time.Since(startTime))
			return err
		}
		// Ensure temporary zip file is cleaned up after upload (or failure)
		defer func() {
			log.Printf("Cleaning up temporary zip: %s", uploadFile)
			if err := os.Remove(uploadFile); err != nil {
				log.Printf("Warning: failed to remove temporary zip: %v", err)
			}
		}()
		log.Printf("Created password-protected zip: %s", filepath.Base(uploadFile))
	}

	// Step 3: Upload to rclone remotes
	if err := uploadToRemotes(cfg, uploadFile); err != nil {
		err = fmt.Errorf("failed to upload backup: %w", err)
		sendDiscordNotification(cfg, false, err.Error(), uploadFile, time.Since(startTime))
		return err
	}

	// Step 4: Prune old backups on remotes
	var warnings []string
	for _, remote := range cfg.RcloneRemotes {
		if err := pruneOldBackups(cfg, remote); err != nil {
			msg := fmt.Sprintf("Failed to prune %s: %v", remote, err)
			log.Printf("Warning: %s", msg)
			warnings = append(warnings, msg)
		}
	}

	duration := time.Since(startTime)
	sendDiscordNotification(cfg, true, strings.Join(warnings, "\n"), uploadFile, duration)
	log.Printf("=== Backup completed successfully (took %v) ===", duration.Round(time.Second))
	return nil
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

// listBackupFiles returns a map of backup file paths to their modification times
func listBackupFiles(dir, pattern string) (map[string]time.Time, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %q: %w", pattern, err)
	}
	files := make(map[string]time.Time, len(matches))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil {
			files[m] = info.ModTime()
		}
	}
	return files, nil
}

// findLatestBackup finds the backup file created by the most recent rake command.
// It compares the current directory state against a pre-rake snapshot to identify
// new or modified files, falling back to an age-based check.
func findLatestBackup(cfg Config, beforeFiles map[string]time.Time) (string, error) {
	log.Println("Step 2: Finding latest backup...")

	pattern := filepath.Join(cfg.BackupDir, cfg.BackupPattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to glob pattern %q: %w", pattern, err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no backup files found matching pattern %q", pattern)
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var allFiles []fileInfo
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			log.Printf("Warning: cannot stat %s: %v", match, err)
			continue
		}
		allFiles = append(allFiles, fileInfo{path: match, modTime: info.ModTime()})
	}

	if len(allFiles) == 0 {
		return "", fmt.Errorf("no accessible backup files found")
	}

	// Identify new or modified files since the pre-rake snapshot
	var newFiles []fileInfo
	for _, f := range allFiles {
		prevModTime, existed := beforeFiles[f.path]
		if !existed || !f.modTime.Equal(prevModTime) {
			newFiles = append(newFiles, f)
		}
	}

	if len(newFiles) > 0 {
		sort.Slice(newFiles, func(i, j int) bool {
			return newFiles[i].modTime.After(newFiles[j].modTime)
		})
		picked := newFiles[0]
		if info, err := os.Stat(picked.path); err == nil {
			log.Printf("Backup verified (new file detected): %s (size: %d bytes)", filepath.Base(picked.path), info.Size())
		} else {
			log.Printf("Backup verified (new file detected): %s (unable to stat: %v)", filepath.Base(picked.path), err)
		}
		return picked.path, nil
	}

	// Fallback: no new/modified files detected, use age-based check
	log.Println("Warning: no new backup file detected after rake command, falling back to age-based check")
	sort.Slice(allFiles, func(i, j int) bool {
		return allFiles[i].modTime.After(allFiles[j].modTime)
	})

	latest := allFiles[0]
	age := time.Since(latest.modTime)
	if age > cfg.MaxAge {
		return "", fmt.Errorf("latest backup %q is too old (age: %v, max: %v)", latest.path, age, cfg.MaxAge)
	}

	if info, err := os.Stat(latest.path); err == nil {
		log.Printf("Backup verified: %s (size: %d bytes, age: %v)", filepath.Base(latest.path), info.Size(), age.Round(time.Second))
	} else {
		log.Printf("Backup verified: %s (age: %v, unable to stat: %v)", filepath.Base(latest.path), age.Round(time.Second), err)
	}
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

// createPasswordZip creates a password-protected zip file from the backup
func createPasswordZip(backupFile, password string) (string, error) {
	log.Println("Step 2.5: Creating password-protected zip...")

	// Output file: same name but with .zip extension
	baseName := filepath.Base(backupFile)
	zipName := baseName + ".zip"
	zipPath := filepath.Join(filepath.Dir(backupFile), zipName)

	fzip, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to create zip file: %w", err)
	}

	// Use closure to ensure files are closed and flushed before stat
	err = func() error {
		defer fzip.Close()

		w := zip.NewWriter(fzip)
		defer w.Close()

		fsrc, err := os.Open(backupFile)
		if err != nil {
			return fmt.Errorf("failed to open source backup: %w", err)
		}
		defer fsrc.Close()

		// Using AES256 for strong security
		wsp, err := w.Encrypt(baseName, password, zip.AES256Encryption)
		if err != nil {
			return fmt.Errorf("failed to create encrypted entry: %w", err)
		}

		if _, err := io.Copy(wsp, fsrc); err != nil {
			return fmt.Errorf("failed to write zip content: %w", err)
		}

		return nil
	}()

	if err != nil {
		return "", err
	}

	// Verify the zip was created
	info, err := os.Stat(zipPath)
	if err != nil {
		return "", fmt.Errorf("zip file not created: %w", err)
	}

	log.Printf("Password-protected zip created: %s (size: %d bytes)", zipName, info.Size())
	return zipPath, nil
}

// rcloneFile represents a file returned by rclone lsjson
type rcloneFile struct {
	Path    string    `json:"Path"`
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

// pruneOldBackups removes old backup files from a remote, keeping only the most recent N
func pruneOldBackups(cfg Config, remote string) error {
	if cfg.NumBackupsToKeep <= 0 {
		return nil
	}

	log.Printf("Pruning old backups on %s (keeping %d)...", remote, cfg.NumBackupsToKeep)

	// List files on remote using rclone lsjson
	args := []string{
		"--config", cfg.RcloneConfig,
		"lsjson",
		remote,
	}

	cmd := exec.Command("rclone", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rclone lsjson failed: %w (stderr: %s)", err, stderr.String())
	}

	var files []rcloneFile
	if err := json.Unmarshal(stdout.Bytes(), &files); err != nil {
		return fmt.Errorf("failed to parse rclone lsjson output: %w", err)
	}

	// Filter to only backup files (matching pattern, excluding directories)
	var backups []rcloneFile
	for _, f := range files {
		if f.IsDir {
			continue
		}
		// Use path.Match for remote paths (always forward slashes)
		// Match the base name against backup pattern (support both .tar and .tar.zip)
		baseName := path.Base(f.Path)
		matched, err := path.Match(cfg.BackupPattern, baseName)
		if err != nil {
			log.Printf("  Warning: invalid backup pattern %q: %v", cfg.BackupPattern, err)
			return fmt.Errorf("invalid backup pattern: %w", err)
		}
		matchedZip, err := path.Match(cfg.BackupPattern+".zip", baseName)
		if err != nil {
			// Pattern+.zip is invalid only if base pattern is already broken
			log.Printf("  Warning: invalid backup pattern %q.zip: %v", cfg.BackupPattern, err)
		}
		if matched || matchedZip {
			backups = append(backups, f)
		}
	}

	if len(backups) <= cfg.NumBackupsToKeep {
		log.Printf("  Found %d backups, no pruning needed", len(backups))
		return nil
	}

	// Sort by ModTime descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ModTime.After(backups[j].ModTime)
	})

	// Delete files beyond the keep limit
	toDelete := backups[cfg.NumBackupsToKeep:]
	log.Printf("  Found %d backups, deleting %d oldest", len(backups), len(toDelete))

	for _, f := range toDelete {
		// Use f.Path for correct remote path (handles subdirectories)
		remotePath := fmt.Sprintf("%s/%s", strings.TrimSuffix(remote, "/"), f.Path)
		log.Printf("  Deleting: %s (age: %v)", f.Path, time.Since(f.ModTime).Round(time.Hour))

		delArgs := []string{
			"--config", cfg.RcloneConfig,
			"deletefile", // Use deletefile for precise single-file deletion
			remotePath,
		}

		delCmd := exec.Command("rclone", delArgs...)
		delCmd.Stderr = os.Stderr

		if err := delCmd.Run(); err != nil {
			log.Printf("  WARNING: Failed to delete %s: %v", f.Path, err)
			// Continue with other deletions
		}
	}

	log.Printf("  Pruning complete")
	return nil
}
