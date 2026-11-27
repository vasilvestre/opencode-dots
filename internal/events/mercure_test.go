package events

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func verify(t *testing.T, header string, secret []byte, role string) {
	t.Helper()
	tok, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		t.Fatalf("authorization = %q, want a bearer token", header)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q, want three parts", tok)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)); parts[2] != want {
		t.Errorf("the token is not signed with the secret")
	}
	if head, _ := base64.RawURLEncoding.DecodeString(parts[0]); !strings.Contains(string(head), `"typ":"at+jwt"`) {
		t.Errorf("header = %s, want typ at+jwt", head)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Details []struct {
			Type    string              `json:"type"`
			Actions []string            `json:"actions"`
			Topics  []map[string]string `json:"topics"`
		} `json:"authorization_details"`
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || len(claims.Details) != 1 ||
		claims.Details[0].Type != "https://mercure.rocks/authorization-detail" || len(claims.Details[0].Actions) != 1 || claims.Details[0].Actions[0] != role {
		t.Errorf("claims = %s, want one Mercure authorization detail (type https://mercure.rocks/authorization-detail) for %s", raw, role)
	}
	if claims.Iss != "dots-test" || claims.Aud == "" || claims.Exp <= time.Now().Unix() {
		t.Errorf("claims = %s, want the issuer, an audience and an expiry in the future (hub 1.1 refuses the token otherwise)", raw)
	}
}

func TestPublishSendsASignedForm(t *testing.T) {
	var got struct {
		topic, data, private, auth string
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got.topic, got.data, got.private, got.auth = r.Form.Get("topic"), r.Form.Get("data"), r.Form.Get("private"), r.Header.Get("Authorization")
		w.Write([]byte("urn:uuid:1"))
	}))
	defer hub.Close()
	h := Hub{URL: hub.URL, Secret: []byte("s3cret"), Issuer: "dots-test"}
	if err := h.Publish(context.Background(), "dots/claim/wi_1", []byte(`{"kind":"created"}`), true); err != nil {
		t.Fatal(err)
	}
	if got.topic != "dots/claim/wi_1" || got.data != `{"kind":"created"}` || got.private != "on" {
		t.Errorf("form = %+v", got)
	}
	verify(t, got.auth, []byte("s3cret"), "publish")
}

func TestPublishReportsARefusal(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad token", http.StatusUnauthorized) }))
	defer hub.Close()
	err := Hub{URL: hub.URL, Secret: []byte("x")}.Publish(context.Background(), "t", []byte("d"), false)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want the status", err)
	}
	if (Hub{}).Publish(context.Background(), "t", nil, false) == nil {
		t.Error("a hub with no URL and no secret was accepted")
	}
}

func TestSubscribeParsesTheStreamAndResumes(t *testing.T) {
	var mu sync.Mutex
	var seen []string // Last-Event-ID of each connection
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verify(t, r.Header.Get("Authorization"), []byte("k"), "subscribe")
		if m := r.URL.Query()["match"]; len(m) != 2 || m[0] != "a" || m[1] != "b" {
			t.Errorf("match parameters = %v", m)
		}
		mu.Lock()
		seen = append(seen, r.Header.Get("Last-Event-ID"))
		n := len(seen)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		if n == 1 {
			fmt.Fprint(w, ": keep-alive\n\nid: e1\ndata: one\n\nid: e2\nevent: claim\ndata: two\ndata: lines\n\n")
			fl.Flush()
			return // the connection drops
		}
		fmt.Fprint(w, "id: e3\ndata: three\n\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer hub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch := Hub{URL: hub.URL, Secret: []byte("k"), Issuer: "dots-test"}.Subscribe(ctx, "a", "b")
	var got []Event
	for ev := range ch {
		got = append(got, ev)
		if len(got) == 3 {
			cancel()
		}
	}
	if len(got) != 3 || got[0].Data != "one" || got[1].Data != "two\nlines" || got[1].Type != "claim" || got[2].ID != "e3" {
		t.Errorf("events = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 || seen[0] != "" || seen[1] != "e2" {
		t.Errorf("Last-Event-ID per connection = %v, want the second to resume from e2", seen)
	}
}

func TestClaimTopic(t *testing.T) {
	if ClaimTopic("wi_9") != "dots/claim/wi_9" {
		t.Errorf("topic = %q", ClaimTopic("wi_9"))
	}
}

func TestSubscriberTokenNamesOnlyItsTopics(t *testing.T) {
	h := Hub{URL: "https://hub.example/.well-known/mercure", Secret: []byte("k"), Issuer: "dots"}
	tok := h.SubscriberToken("octo", "http://hub.example/.well-known/mercure", []string{RepoTopic("Org/Repo"), RepoTopic("org/b")}, time.Hour)
	parts := strings.Split(tok, ".")
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if parts[2] != base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) {
		t.Fatal("the signature does not verify with the hub key")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	var c struct {
		Sub, Aud, Iss string
		Exp, Iat      int64
		Details       []struct {
			Actions []string
			Topics  []map[string]string
		} `json:"authorization_details"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Sub != "octo" || c.Aud != "http://hub.example/.well-known/mercure" || c.Iss != "dots" {
		t.Fatalf("claims = %+v", c)
	}
	if got := c.Exp - c.Iat; got != 3600 {
		t.Fatalf("lifetime = %ds, want 3600", got)
	}
	if len(c.Details) != 1 || len(c.Details[0].Actions) != 1 || c.Details[0].Actions[0] != "subscribe" {
		t.Fatalf("details = %+v", c.Details)
	}
	want := []string{"dots/repo/org/repo", "dots/repo/org/b"}
	for i, topic := range c.Details[0].Topics {
		if topic["match"] != want[i] || topic["match_type"] != "exact" {
			t.Fatalf("topic %d = %v, want exact %s", i, topic, want[i])
		}
	}
	if len(c.Details[0].Topics) != len(want) {
		t.Fatalf("topics = %v", c.Details[0].Topics)
	}
}
