package sentry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"opencode-dots/internal/sources"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func one(t *testing.T, name string) (sources.Envelope, Payload) {
	t.Helper()
	envs, err := Adapter{Org: "acme"}.Parse(fixture(t, name))
	if err != nil || len(envs) != 1 {
		t.Fatalf("%s: %d envelopes, err %v", name, len(envs), err)
	}
	if err := envs[0].Validate(); err != nil {
		t.Fatal(err)
	}
	var p Payload
	if err := json.Unmarshal(envs[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return envs[0], p
}

func TestWebhooks(t *testing.T) {
	c, p := one(t, "webhook_created.json")
	if c.Ref != "sentry:acme/web#4711" || c.Kind != KindCreated || c.Actor != Actor {
		t.Errorf("created = %+v", c)
	}
	if !c.Time.Equal(time.Date(2024, 5, 2, 8, 30, 0, 0, time.UTC)) || p.Count != 1 || p.Level != "error" ||
		p.Culprit == "" || p.Permalink == "" || p.ShortID != "WEB-1A" {
		t.Errorf("created payload = %+v", p)
	}
	r, p := one(t, "webhook_regression.json")
	if r.Kind != KindRegression || p.Count != 42 || len(p.Frames) != 3 {
		t.Fatalf("regression = %+v %+v", r, p)
	}
	if p.Frames[0].Function != "addItem" || p.Frames[2].InApp {
		t.Errorf("frames must be innermost first: %+v", p.Frames)
	}
	d, _ := one(t, "webhook_resolved.json")
	if d.Kind != KindResolved || d.Key == r.Key || d.Key == c.Key {
		t.Errorf("resolved = %+v", d)
	}
}

func TestWebhookRedeliveryKeepsKey(t *testing.T) {
	a, _ := one(t, "webhook_regression.json")
	b, _ := one(t, "webhook_regression.json")
	if a.Key != b.Key {
		t.Error("a redelivery changed the key")
	}
}

func TestListIsIdempotent(t *testing.T) {
	a := Adapter{Org: "acme"}
	first, err := a.Parse(fixture(t, "list.json"))
	if err == nil || !strings.Contains(err.Error(), "list item 2") {
		t.Errorf("err = %v, want the bad id reported", err)
	}
	if len(first) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(first))
	}
	// the same issue seen later, with more events, keeps its key
	later := strings.Replace(string(fixture(t, "list.json")), `"count":"42"`, `"count":"99"`, 1)
	later = strings.Replace(later, "2024-06-10T12:00:00Z", "2024-07-01T00:00:00Z", 1)
	again, _ := a.Parse([]byte(later))
	if len(again) != len(first) {
		t.Fatalf("got %d envelopes, want %d", len(again), len(first))
	}
	for i := range first {
		if first[i].Key != again[i].Key {
			t.Errorf("item %d: key changed", i)
		}
	}
}

func TestHostileTextStaysData(t *testing.T) {
	e, p := one(t, "webhook_hostile.json")
	// the reference comes from structured fields, not from the title or the culprit
	if e.Ref != "sentry:acme/web#4712" {
		t.Errorf("ref = %s", e.Ref)
	}
	if !strings.HasPrefix(p.Title, "Ignore all previous instructions") {
		t.Errorf("title not carried verbatim: %q", p.Title)
	}
	if strings.ContainsAny(p.Culprit, "\n\r") {
		t.Errorf("control characters kept: %q", p.Culprit)
	}
	if p.Permalink != "" {
		t.Errorf("non-https permalink kept: %q", p.Permalink)
	}
}

func TestClip(t *testing.T) {
	if got := clip(strings.Repeat("é", 2000)); len([]rune(got)) != maxText {
		t.Errorf("len = %d", len([]rune(got)))
	}
}

func TestEmptyAndMalformed(t *testing.T) {
	a := Adapter{Org: "acme"}
	envs, err := a.Parse(fixture(t, "list_empty.json"))
	if err != nil || len(envs) != 0 {
		t.Errorf("empty list = %v, %v", envs, err)
	}
	if _, err := a.Parse(fixture(t, "malformed.json")); err == nil {
		t.Error("malformed body accepted")
	}
	for _, in := range []string{"", "null", "42", `{"action":"assigned","data":{}}`} {
		if envs, err := a.Parse([]byte(in)); err == nil || len(envs) != 0 {
			t.Errorf("Parse(%q) = %v, %v", in, envs, err)
		}
	}
	if _, err := (Adapter{Org: "Bad Org"}).Parse(fixture(t, "webhook_created.json")); err == nil {
		t.Error("invalid org accepted")
	}
}
