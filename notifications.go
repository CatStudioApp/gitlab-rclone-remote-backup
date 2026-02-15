package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sendDiscordNotification sends a notification to Discord webhook
func sendDiscordNotification(cfg Config, success bool, message string, backupFile string, duration time.Duration) {
	if cfg.DiscordWebhookURL == "" {
		return
	}

	hostname, _ := os.Hostname()
	footer := map[string]interface{}{
		"text": fmt.Sprintf("Host: %s • Container: %s", hostname, cfg.GitLabContainerName),
	}

	var embed map[string]interface{}

	if success {
		fields := []map[string]interface{}{
			{"name": "📦 File", "value": filepath.Base(backupFile), "inline": true},
			{"name": "⏱️ Duration", "value": duration.Round(time.Second).String(), "inline": true},
			{"name": "☁️ Remotes", "value": strings.Join(cfg.RcloneRemotes, "\n"), "inline": false},
		}

		if cfg.NumBackupsToKeep > 0 {
			fields = append(fields, map[string]interface{}{
				"name":   "🗑️ Retention",
				"value":  fmt.Sprintf("Keeping last %d backups per remote", cfg.NumBackupsToKeep),
				"inline": true,
			})
		}

		// If there are non-fatal warnings/messages, add them
		if message != "" {
			fields = append(fields, map[string]interface{}{
				"name":   "⚠️ Warnings",
				"value":  truncate(message, 1000),
				"inline": false,
			})
		}

		embed = map[string]interface{}{
			"title":       "✅ GitLab Backup Successful",
			"color":       0x00FF00, // Green
			"description": "Backup completed and uploaded successfully.",
			"fields":      fields,
			"footer":      footer,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}
	} else {
		// Parse which step failed from the error message
		failedStep := detectFailedStep(message)

		fields := []map[string]interface{}{
			{"name": "🔴 Failed Step", "value": failedStep, "inline": true},
			{"name": "⏱️ Duration", "value": duration.Round(time.Second).String(), "inline": true},
		}

		if backupFile != "" {
			fields = append(fields, map[string]interface{}{
				"name":   "📦 Backup File",
				"value":  filepath.Base(backupFile),
				"inline": true,
			})
		}

		fields = append(fields, map[string]interface{}{
			"name":   "❌ Error Details",
			"value":  fmt.Sprintf("```\n%s\n```", truncate(message, 900)),
			"inline": false,
		})

		embed = map[string]interface{}{
			"title":       "❌ GitLab Backup Failed",
			"color":       0xFF0000, // Red
			"description": fmt.Sprintf("Backup failed at step: **%s**", failedStep),
			"fields":      fields,
			"footer":      footer,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{embed},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Warning: failed to marshal Discord payload: %v", err)
		return
	}

	resp, err := http.Post(cfg.DiscordWebhookURL, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		log.Printf("Warning: failed to send Discord notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Warning: Discord webhook returned status %d: %s", resp.StatusCode, string(body))
		return
	}

	log.Println("Discord notification sent")
}

// detectFailedStep infers the failed step from the error message
func detectFailedStep(errMsg string) string {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "create gitlab backup"),
		strings.Contains(lower, "docker client"),
		strings.Contains(lower, "exec"),
		strings.Contains(lower, "backup command exited"):
		return "Create GitLab Backup (gitlab-rake)"
	case strings.Contains(lower, "find latest backup"),
		strings.Contains(lower, "no backup files"),
		strings.Contains(lower, "too old"):
		return "Find Latest Backup"
	case strings.Contains(lower, "password zip"),
		strings.Contains(lower, "encrypted entry"),
		strings.Contains(lower, "zip"):
		return "Create Password-Protected Zip"
	case strings.Contains(lower, "upload"),
		strings.Contains(lower, "rclone"):
		return "Upload to Rclone Remotes"
	default:
		return "Unknown"
	}
}
