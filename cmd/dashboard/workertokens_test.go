package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opencode-dots/internal/events"
	"opencode-dots/internal/workertokens"
)

func workerRig(t *testing.T) *rig {
	r := newRig(t)
	withHub(r)
	r.d.wtokens.FailDelay = 0
	return r
}

// issueToken has the owner issue a token and returns its secret and id.
func issueToken(t *testing.T, r *rig, body string) (secret, id string) {
	t.Helper()
	rec := ask(r.h, "POST", "/api/worker-tokens", "owner", "", body)
	if rec.Code != 200 {
		t.Fatalf("issue = %d %s", rec.Code, rec.Body)
	}
	var out struct{ Token, ID string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Token, workertokens.Prefix) || out.ID == "" {
		t.Fatalf("issue answer = %s", rec.Body)
	}
	return out.Token, out.ID
}

func exchange(r *rig, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/hub-token", nil)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	rec := httptest.NewRecorder()
	r.d.workerHandler().ServeHTTP(rec, req)
	return rec
}

func TestWorkerTokenTradesForAHubTokenOfItsRepositories(t *testing.T) {
	r := workerRig(t)
	secret, _ := issueToken(t, r, `{"label":"laptop-ada","login":"ada","repos":["`+openRepo+`"]}`)
	rec := exchange(r, secret)
	if rec.Code != 200 {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body)
	}
	var got hubReply
	json.Unmarshal(rec.Body.Bytes(), &got)
	sub, _, topics := hubClaims(t, got.Token)
	if sub != "ada" || len(topics) != 1 || topics[0] != events.RepoTopic(openRepo) {
		t.Fatalf("sub=%q topics=%v, want ada and only %s", sub, topics, events.RepoTopic(openRepo))
	}
}

func TestWorkerTokenWithNoRepositoryListCoversTheKnownOnes(t *testing.T) {
	r := workerRig(t)
	secret, _ := issueToken(t, r, `{"label":"ci"}`)
	var got hubReply
	json.Unmarshal(exchange(r, secret).Body.Bytes(), &got)
	sub, _, topics := hubClaims(t, got.Token)
	if sub != "worker:ci" || len(topics) < 2 {
		t.Fatalf("sub=%q topics=%v", sub, topics)
	}
}

func TestWorkerTokenRefusesWhatIsNotLive(t *testing.T) {
	r := workerRig(t)
	secret, id := issueToken(t, r, `{"label":"a"}`)
	for name, tok := range map[string]string{"none": "", "unknown": workertokens.Prefix + "nope", "wrong prefix": "x" + secret} {
		if got := exchange(r, tok).Code; got != 401 {
			t.Errorf("%s = %d, want 401", name, got)
		}
	}
	if rec := ask(r.h, "POST", "/api/worker-tokens/revoke", "owner", "", `{"id":"`+id+`"}`); rec.Code != 200 {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	if got := exchange(r, secret).Code; got != 401 {
		t.Fatalf("a revoked token = %d, want 401", got)
	}
	if rec := ask(r.h, "POST", "/api/worker-tokens/revoke", "owner", "", `{"id":"`+id+`"}`); rec.Code != 404 {
		t.Fatalf("revoking twice = %d, want 404", rec.Code)
	}
	// An expired token stops too.
	s2, _ := issueToken(t, r, `{"label":"b","days":1}`)
	r.d.wtokens.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if got := exchange(r, s2).Code; got != 401 {
		t.Fatalf("an expired token = %d, want 401", got)
	}
}

func TestWorkerTokenIssuingIsOwnerOnlyAndBounded(t *testing.T) {
	r := workerRig(t)
	r.signIn()
	if rec := ask(r.h, "POST", "/api/worker-tokens", "visitor", r.cookie, `{"label":"x"}`); rec.Code != 403 {
		t.Fatalf("a visitor issuing = %d, want 403", rec.Code)
	}
	if rec := ask(r.h, "GET", "/api/worker-tokens", "visitor", r.cookie, ""); rec.Code != 403 {
		t.Fatalf("a visitor listing = %d, want 403", rec.Code)
	}
	for name, body := range map[string]string{
		"no label":        `{"label":""}`,
		"unknown repo":    `{"label":"x","repos":["nobody/nothing"]}`,
		"over 90 days":    `{"label":"x","days":91}`,
		"malformed input": `{`,
	} {
		if rec := ask(r.h, "POST", "/api/worker-tokens", "owner", "", body); rec.Code != 400 {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
}

func TestWorkerTokensAreKeptHashedAndPrivate(t *testing.T) {
	r := workerRig(t)
	dir := t.TempDir()
	r.d.wtokens = workertokens.New(dir)
	r.d.wtokens.FailDelay = 0
	secret, _ := issueToken(t, r, `{"label":"laptop"}`)
	path := filepath.Join(dir, workertokens.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), strings.TrimPrefix(secret, workertokens.Prefix)) {
		t.Fatal("the file holds the secret")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// A restart keeps the token; a listing never shows the secret.
	again := workertokens.New(dir)
	if _, ok := again.Lookup(secret); !ok {
		t.Fatal("the token did not survive a restart")
	}
	rec := ask(r.h, "GET", "/api/worker-tokens", "owner", "", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
}

func TestWorkerListenerServesOnlyTheExchange(t *testing.T) {
	r := workerRig(t)
	for _, path := range []string{"/", "/api/home", "/api/worker-tokens", "/api/session", "/auth/logout"} {
		rec := httptest.NewRecorder()
		r.d.workerHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 404 {
			t.Errorf("worker listener %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestCheckWorkerAddr(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:8096": true, "[::1]:8096": true, "0.0.0.0:8096": false,
		"localhost:8096": false, "8096": false, "127.0.0.1:8099": false, "127.0.0.1:8098": false} {
		if err := checkWorkerAddr(addr, "127.0.0.1:8099", "127.0.0.1:8098"); (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%v", addr, err, ok)
		}
	}
}
