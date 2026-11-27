package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/events"
)

type hubReply struct {
	Hub    string   `json:"hub"`
	Token  string   `json:"token"`
	Topics []string `json:"topics"`
}

func hubClaims(t *testing.T, token string) (sub, aud string, topics []string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q", token)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Sub, Aud string
		Details  []struct {
			Topics []map[string]string
		} `json:"authorization_details"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for _, d := range c.Details {
		for _, tp := range d.Topics {
			topics = append(topics, tp["match"])
		}
	}
	return c.Sub, c.Aud, topics
}

func withHub(r *rig) {
	r.d.hub = &hubIssuer{url: "https://hub.example/.well-known/mercure", audience: "http://hub.example/.well-known/mercure",
		hub: events.Hub{URL: "https://hub.example/.well-known/mercure", Secret: []byte("k"), Issuer: "dots"}}
}

func TestHubTokenIsOffWithoutAHub(t *testing.T) {
	r := newRig(t)
	r.signIn()
	if got := r.visitor("/api/hub-token").Code; got != 404 {
		t.Fatalf("no hub configured: %d, want 404", got)
	}
}

func TestHubTokenNeedsASignedInVisitor(t *testing.T) {
	r := newRig(t)
	withHub(r)
	if got := ask(r.h, "GET", "/api/hub-token", "visitor", "", "").Code; got != 401 {
		t.Fatalf("no session: %d, want 401", got)
	}
}

func TestHubTokenCarriesOnlyTheVisitorsRepositories(t *testing.T) {
	r := newRig(t)
	withHub(r)
	r.signIn()
	rec := r.visitor("/api/hub-token")
	if rec.Code != 200 {
		t.Fatalf("hub token = %d %s", rec.Code, rec.Body)
	}
	var got hubReply
	json.Unmarshal(rec.Body.Bytes(), &got)
	sub, aud, topics := hubClaims(t, got.Token)
	if len(topics) != 1 || topics[0] != events.RepoTopic(openRepo) {
		t.Fatalf("topics = %v, want only %s", topics, events.RepoTopic(openRepo))
	}
	for _, tp := range got.Topics {
		if strings.Contains(tp, strings.ToLower(hiddenRepo)) {
			t.Fatalf("the hidden repository leaked: %v", got.Topics)
		}
	}
	if sub != "alice" || aud != "http://hub.example/.well-known/mercure" || got.Hub != "https://hub.example/.well-known/mercure" {
		t.Fatalf("sub=%q aud=%q hub=%q", sub, aud, got.Hub)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a token must not be cached")
	}
}

func TestHubTokenOfTheOwnerCoversEveryRepository(t *testing.T) {
	r := newRig(t)
	withHub(r)
	rec := r.owner("/api/hub-token")
	if rec.Code != 200 {
		t.Fatalf("owner = %d %s", rec.Code, rec.Body)
	}
	var got hubReply
	json.Unmarshal(rec.Body.Bytes(), &got)
	if sub, _, _ := hubClaims(t, got.Token); sub != "owner" {
		t.Fatalf("sub = %q", sub)
	}
	want := map[string]bool{events.RepoTopic(openRepo): true, events.RepoTopic(hiddenRepo): true}
	for _, tp := range got.Topics {
		delete(want, tp)
	}
	if len(want) != 0 {
		t.Fatalf("topics %v miss %v", got.Topics, want)
	}
}

func TestHubIssuerReadsTheHubSettings(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hub.env")
	os.WriteFile(f, []byte("# keys\nMERCURE_SUBSCRIBER_JWT_KEY=abc\nMERCURE_TRUSTED_ISSUERS=dots other\n"), 0o600)
	h, err := newHubIssuer("https://h/.well-known/mercure", "", "", f)
	if err != nil || h == nil {
		t.Fatalf("issuer = %v, %v", h, err)
	}
	if h.audience != "https://h/.well-known/mercure" || h.hub.Issuer != "dots" || string(h.hub.Secret) != "abc" {
		t.Fatalf("issuer = %+v", h)
	}
	if off, err := newHubIssuer("", "", "", f); off != nil || err != nil {
		t.Fatalf("no URL must mean off: %v, %v", off, err)
	}
	if _, err := newHubIssuer("https://h", "", "", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing settings file must be an error, not a silent off")
	}
}
