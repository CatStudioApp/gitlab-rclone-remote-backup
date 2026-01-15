package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// sendDiscordNotification sends a notification to Discord webhook
func sendDiscordNotification(cfg Config, success bool, errorMsg string, backupFile string, duration time.Duration) {
	if cfg.DiscordWebhookURL == "" {
		return
	}

	var embed map[string]interface{}

	if success {
		embed = map[string]interface{}{
			"title":       "✅ GitLab Backup Successful",
			"color":       0x00FF00, // Green
			"description": fmt.Sprintf("Backup completed and uploaded successfully."),
			"fields": []map[string]interface{}{
				{"name": "File", "value": filepath.Base(backupFile), "inline": true},
				{"name": "Duration", "value": duration.Round(time.Second).String(), "inline": true},
				{"name": "Remotes", "value": strings.Join(cfg.RcloneRemotes, ", "), "inline": false},
			},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
	} else {
		embed = map[string]interface{}{
			"title":       "❌ GitLab Backup Failed",
			"color":       0xFF0000, // Red
			"description": fmt.Sprintf("Backup failed with error."),
			"fields": []map[string]interface{}{
				{"name": "Error", "value": truncate(errorMsg, 1000), "inline": false},
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
