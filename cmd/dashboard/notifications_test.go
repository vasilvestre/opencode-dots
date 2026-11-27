package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testWebhook = "https://discord.com/api/webhooks/123/token"

func notificationRequest(t *testing.T, d *dash, method, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/notifications", nil)
	} else {
		req = httptest.NewRequest(method, "/api/notifications", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Host = "127.0.0.1:8099"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	d.notifications(rec, req)
	return rec
}

func TestNotificationsDefaults(t *testing.T) {
	d := &dash{root: t.TempDir()}
	rec := notificationRequest(t, d, http.MethodGet, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET returned %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"discord":{"enabled":false,"webhook":""}}` {
		t.Fatalf("GET returned %q, want the disabled shape", got)
	}
}

func TestNotificationsPutValid(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	body := `{"discord":{"enabled":true,"webhook":"` + testWebhook + `"}}`
	rec := notificationRequest(t, d, http.MethodPut, body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT returned %d: %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != body {
		t.Fatalf("PUT returned %q, want %q", got, body)
	}
	data, err := os.ReadFile(filepath.Join(root, "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != body {
		t.Fatalf("saved file = %q, want %q", got, body)
	}
	rec = notificationRequest(t, d, http.MethodGet, "", "")
	if got := strings.TrimSpace(rec.Body.String()); got != body {
		t.Fatalf("GET after PUT returned %q, want %q", got, body)
	}

	// The legacy host stays valid for webhooks created before discord.com.
	legacy := `{"discord":{"enabled":false,"webhook":"https://discordapp.com/api/webhooks/1/x"}}`
	if rec := notificationRequest(t, d, http.MethodPut, legacy, ""); rec.Code != http.StatusOK {
		t.Fatalf("legacy webhook PUT returned %d: %s", rec.Code, rec.Body)
	}
}

func TestNotificationsRejectsBadConfig(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	valid := `{"discord":{"enabled":true,"webhook":"` + testWebhook + `"}}`
	if rec := notificationRequest(t, d, http.MethodPut, valid, ""); rec.Code != http.StatusOK {
		t.Fatalf("initial PUT returned %d: %s", rec.Code, rec.Body)
	}

	cases := []struct {
		name, body string
	}{
		{"foreign webhook", `{"discord":{"enabled":true,"webhook":"https://evil.example/hook"}}`},
		{"bad type", `{"discord":{"enabled":"yes","webhook":""}}`},
		{"unknown top level key", `{"discord":{"enabled":false,"webhook":""},"email":{}}`},
		{"unknown nested key", `{"discord":{"enabled":false,"webhook":"","email":"x"}}`},
		{"missing discord", `{}`},
		{"trailing object", valid + ` {}`},
		{"overlong webhook", `{"discord":{"enabled":false,"webhook":"` + testWebhook + strings.Repeat("a", 512) + `"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := notificationRequest(t, d, http.MethodPut, tc.body, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT returned %d: %s", rec.Code, rec.Body)
			}
			data, _ := os.ReadFile(filepath.Join(root, "notifications.json"))
			if got := strings.TrimSpace(string(data)); got != valid {
				t.Fatalf("refused PUT changed the file to %q", got)
			}
		})
	}
}

func TestNotificationsRejectsForeignOrigin(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := notificationRequest(t, d, method, "", "https://evil.example")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a foreign origin returned %d: %s", method, rec.Code, rec.Body)
		}
	}
	rec := notificationRequest(t, d, http.MethodPost, "", "")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("POST returned %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestNotificationsBodyCap(t *testing.T) {
	d := &dash{root: t.TempDir()}
	body := `{"discord":{"enabled":false,"webhook":"` + strings.Repeat("a", maxNotificationsBodySize+1) + `"}}`
	rec := notificationRequest(t, d, http.MethodPut, body, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized PUT returned %d: %s", rec.Code, rec.Body)
	}
}

func TestNotificationsBackup(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	first := `{"discord":{"enabled":false,"webhook":""}}`
	second := `{"discord":{"enabled":true,"webhook":"` + testWebhook + `"}}`
	notificationRequest(t, d, http.MethodPut, first, "")
	notificationRequest(t, d, http.MethodPut, second, "")

	matches, err := filepath.Glob(filepath.Join(root, "notifications.json.*.bak"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("backups = %v (err %v), want one", matches, err)
	}
	backup, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(backup)); got != first {
		t.Fatalf("backup = %q, want %q", got, first)
	}
}

func TestNotifyRunPostsToTheConfiguredWebhook(t *testing.T) {
	root := t.TempDir()
	cfg, _ := json.Marshal(notificationConfig{Discord: discordNotification{Enabled: true, Webhook: testWebhook}})
	os.WriteFile(filepath.Join(root, "notifications.json"), cfg, 0o644)

	var sent []string
	d := &dash{root: root, discordPost: func(webhook, message string) error {
		sent = append(sent, webhook+"|"+message)
		return nil
	}}
	d.notifyRun("run-1", "review-pr", "completed")
	if len(sent) != 1 || sent[0] != testWebhook+"|dots: review-pr completed" {
		t.Fatalf("sent %v, want the webhook, the workflow and the status", sent)
	}

	// Reading the config at send time means a save takes effect at once.
	os.Remove(filepath.Join(root, "notifications.json"))
	d.notifyRun("run-2", "reply-pr", "completed")
	if len(sent) != 1 {
		t.Fatalf("disabled config still sent %v", sent)
	}
}

func TestPostDiscord(t *testing.T) {
	var got struct {
		method, contentType, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method = r.Method
		got.contentType = r.Header.Get("Content-Type")
		got.body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := postDiscord(srv.URL, "dots: review-pr completed"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.contentType != "application/json" {
		t.Fatalf("request = %s %q", got.method, got.contentType)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload["content"] != "dots: review-pr completed" {
		t.Fatalf("payload = %q", got.body)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer failing.Close()
	if err := postDiscord(failing.URL, "hi"); err == nil {
		t.Fatal("a rejected webhook returned no error")
	}
}
