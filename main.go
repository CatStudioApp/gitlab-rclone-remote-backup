package main

import "log"

func main() {
	cfg := parseFlags()

	log.Println("=== GitLab Backup Tool ===")
	log.Printf("Container: %s", cfg.GitLabContainerName)
	log.Printf("Backup Dir: %s", cfg.BackupDir)
	log.Printf("Rclone Remotes: %v", cfg.RcloneRemotes)
	if cfg.ZipPassword != "" {
		log.Println("Password protection: enabled")
	}

	// Check for manual run first
	if cfg.RunOnce {
		log.Println("Manual backup triggered via --now flag")
		if err := runBackup(cfg); err != nil {
			log.Fatalf("Manual backup failed: %v", err)
		}
		return
	}

	// If cron schedule is set, run as daemon
	if cfg.CronSchedule != "" {
		runWithScheduler(cfg)
		return
	}

	// Otherwise, run once and exit (default behavior)
	if err := runBackup(cfg); err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
}
