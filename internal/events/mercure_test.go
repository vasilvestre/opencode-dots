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
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Mercure map[string][]string `json:"mercure"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || len(claims.Mercure[role]) == 0 {
		t.Errorf("claims = %s, want the mercure %s role", raw, role)
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
	h := Hub{URL: hub.URL, Secret: []byte("s3cret")}
	if err := h.Publish(context.Background(), "dots/claim/wi_1", []byte(`{"kind":"created"}`), true); err != nil {
		t.Fatal(err)
	}
	if got.topic != "dots/claim/wi_1" || got.data != `{"kind":"created"}` || got.private != "on" {
		t.Errorf("form = %+v", got)
	}
	verify(t, got.auth, []byte("s3cret"), "publish")
}

func TestPublishReportsARefusal(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad token", 401) }))
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
		if r.URL.Query()["topic"][0] != "a" || r.URL.Query()["topic"][1] != "b" {
			t.Errorf("topics = %v", r.URL.Query()["topic"])
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
	ch := Hub{URL: hub.URL, Secret: []byte("k")}.Subscribe(ctx, "a", "b")
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
