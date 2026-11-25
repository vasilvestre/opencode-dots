package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const maxNotificationsBodySize = 16 << 10
const maxWebhookLength = 512

const (
	discordWebhookPrefix    = "https://discord.com/api/webhooks/"
	discordWebhookLegacy    = "https://discordapp.com/api/webhooks/"
	notificationsConfigFile = "notifications.json"
)

// notificationConfig is the private config the Config view edits. The webhook
// is a channel URL, not a secret held in the environment.
type notificationConfig struct {
	Discord discordNotification `json:"discord"`
}

type discordNotification struct {
	Enabled bool   `json:"enabled"`
	Webhook string `json:"webhook"`
}

func defaultNotificationConfig() notificationConfig {
	return notificationConfig{}
}

// readNotificationConfig reads the saved config at use time, so a save in the
// Config view takes effect without a restart. A missing or broken file reads as
// disabled.
func readNotificationConfig(root string) notificationConfig {
	cfg := defaultNotificationConfig()
	loadJSON(filepath.Join(root, notificationsConfigFile), &cfg)
	return cfg
}

func (d *dash) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or PUT only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "refused origin"})
		return
	}

	var cfg notificationConfig
	if r.Method == http.MethodPut {
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/json"})
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotificationsBodySize))
		decoder.DisallowUnknownFields()
		var req struct {
			Discord *discordNotification `json:"discord"`
		}
		if err := decoder.Decode(&req); err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeJSONStatus(w, status, map[string]string{"error": "bad body: " + err.Error()})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad body: expected one JSON object"})
			return
		}
		if req.Discord == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "discord object is required"})
			return
		}
		cfg = notificationConfig{Discord: *req.Discord}
		if len(cfg.Discord.Webhook) > maxWebhookLength {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "webhook is too long"})
			return
		}
		if webhook := cfg.Discord.Webhook; webhook != "" &&
			!strings.HasPrefix(webhook, discordWebhookPrefix) &&
			!strings.HasPrefix(webhook, discordWebhookLegacy) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "webhook must be a Discord URL"})
			return
		}
	} else {
		writeJSON(w, readNotificationConfig(d.root))
		return
	}

	d.editMu.Lock()
	defer d.editMu.Unlock()
	path := filepath.Join(d.root, notificationsConfigFile)
	_, err := backupFile(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	data, _ := json.Marshal(cfg)
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, cfg)
}

// postDiscord sends one message to a channel webhook. A dead webhook or a slow
// network is logged by the caller and never stops the recorder.
func postDiscord(webhook, message string) error {
	payload, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("discord answered %s", resp.Status)
	}
	return nil
}
