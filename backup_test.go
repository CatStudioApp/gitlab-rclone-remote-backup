package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createTempBackup creates a temp file matching the GitLab backup naming pattern
// and sets its mtime to the given time.
func createTempBackup(t *testing.T, dir, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake-backup-data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListBackupFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	createTempBackup(t, dir, "111_gitlab_backup.tar", now.Add(-2*time.Hour))
	createTempBackup(t, dir, "222_gitlab_backup.tar", now.Add(-1*time.Hour))
	// non-matching file should be excluded
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0644)

	files, err := listBackupFiles(dir, "*_gitlab_backup.tar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	for path := range files {
		if filepath.Ext(path) != ".tar" {
			t.Errorf("unexpected file in result: %s", path)
		}
	}
}

func TestFindLatestBackup_NewFileDetected(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	oldTime := now.Add(-24 * time.Hour)

	// Simulate a pre-existing old backup
	oldPath := createTempBackup(t, dir, "old_gitlab_backup.tar", oldTime)

	// Snapshot before rake
	beforeFiles, _ := listBackupFiles(dir, "*_gitlab_backup.tar")

	// Simulate rake creating a new backup (even with an old mtime — the bug scenario)
	createTempBackup(t, dir, "new_gitlab_backup.tar", oldTime)

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	result, err := findLatestBackup(cfg, beforeFiles)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	expected := filepath.Join(dir, "new_gitlab_backup.tar")
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// The old file should NOT have been picked despite being the only option before
	if result == oldPath {
		t.Error("should not have picked the pre-existing old file")
	}
}

func TestFindLatestBackup_ModifiedFileDetected(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	oldTime := now.Add(-24 * time.Hour)

	// Pre-existing file with old mtime
	path := createTempBackup(t, dir, "backup_gitlab_backup.tar", oldTime)

	// Snapshot before rake
	beforeFiles, _ := listBackupFiles(dir, "*_gitlab_backup.tar")

	// Simulate rake overwriting the file (mtime changes)
	newTime := now.Add(-23 * time.Hour) // still "old" but different from before
	os.Chtimes(path, newTime, newTime)

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	result, err := findLatestBackup(cfg, beforeFiles)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if result != path {
		t.Errorf("expected %s, got %s", path, result)
	}
}

func TestFindLatestBackup_FallbackAgeCheck_Passes(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// File exists both before and after with same mtime — no diff detected
	createTempBackup(t, dir, "backup_gitlab_backup.tar", now.Add(-30*time.Minute))

	beforeFiles, _ := listBackupFiles(dir, "*_gitlab_backup.tar")

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	result, err := findLatestBackup(cfg, beforeFiles)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}

	expected := filepath.Join(dir, "backup_gitlab_backup.tar")
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestFindLatestBackup_FallbackAgeCheck_TooOld(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// File exists with same mtime before and after — no diff — and it's too old
	createTempBackup(t, dir, "backup_gitlab_backup.tar", now.Add(-5*time.Hour))

	beforeFiles, _ := listBackupFiles(dir, "*_gitlab_backup.tar")

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	_, err := findLatestBackup(cfg, beforeFiles)
	if err == nil {
		t.Fatal("expected error for too-old backup, got nil")
	}

	if got := err.Error(); !strings.Contains(got, "too old") {
		t.Errorf("expected 'too old' in error, got: %s", got)
	}
}

func TestFindLatestBackup_NoFiles(t *testing.T) {
	dir := t.TempDir()

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	_, err := findLatestBackup(cfg, nil)
	if err == nil {
		t.Fatal("expected error for no files, got nil")
	}
}

func TestFindLatestBackup_MultipleNewFiles_PicksNewest(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	beforeFiles := make(map[string]time.Time) // empty — all files are "new"

	createTempBackup(t, dir, "aaa_gitlab_backup.tar", now.Add(-2*time.Hour))
	createTempBackup(t, dir, "bbb_gitlab_backup.tar", now.Add(-1*time.Hour))
	createTempBackup(t, dir, "ccc_gitlab_backup.tar", now.Add(-30*time.Minute))

	cfg := Config{
		BackupDir:     dir,
		BackupPattern: "*_gitlab_backup.tar",
		MaxAge:        1 * time.Hour,
	}

	result, err := findLatestBackup(cfg, beforeFiles)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	expected := filepath.Join(dir, "ccc_gitlab_backup.tar")
	if result != expected {
		t.Errorf("expected newest file %s, got %s", expected, result)
	}
}

