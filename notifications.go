package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// sendDiscordNotification sends a notification to Discord webhook
func sendDiscordNotification(cfg Config, success bool, message string, backupFile string, duration time.Duration) {
	if cfg.DiscordWebhookURL == "" {
		return
	}

	var embed map[string]interface{}

	if success {
		fields := []map[string]interface{}{
			{"name": "File", "value": filepath.Base(backupFile), "inline": true},
			{"name": "Duration", "value": duration.Round(time.Second).String(), "inline": true},
			{"name": "Remotes", "value": strings.Join(cfg.RcloneRemotes, ", "), "inline": false},
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
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}
	} else {
		embed = map[string]interface{}{
			"title":       "❌ GitLab Backup Failed",
			"color":       0xFF0000, // Red
			"description": "Backup failed with error.",
			"fields": []map[string]interface{}{
				{"name": "Error", "value": truncate(message, 1000), "inline": false},
				{"name": "Duration", "value": duration.Round(time.Second).String(), "inline": true},
			},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
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
