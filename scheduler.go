package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"
)

// runWithScheduler starts the cron scheduler and blocks until SIGTERM/SIGINT
func runWithScheduler(cfg Config) {
	log.Printf("Cron schedule: %s", cfg.CronSchedule)
	log.Println("Starting scheduler daemon...")

	c := cron.New(cron.WithLogger(cron.VerbosePrintfLogger(log.Default())))

	_, err := c.AddFunc(cfg.CronSchedule, func() {
		log.Println("Scheduled backup triggered")
		if err := runBackup(cfg); err != nil {
			log.Printf("Scheduled backup failed: %v", err)
		}
	})
	if err != nil {
		log.Fatalf("Invalid cron schedule %q: %v", cfg.CronSchedule, err)
	}

	c.Start()
	log.Printf("Scheduler started. Next run: %v", c.Entries()[0].Next)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down scheduler...")
	ctx := c.Stop()
	<-ctx.Done()
	log.Println("Scheduler stopped")
}
