package sources

import (
	"encoding/json"
	"testing"
)

func TestParseRef(t *testing.T) {
	good := []string{
		"github:acme/shop#12", "branch:acme/shop:feat/x-1", "jira:ABC-12",
		"sentry:acme/web#4711", "advisory:GHSA-cfgh-2345-wxvr@acme/app", "advisory:CVE-2024-12345@acme/shop.v2",
	}
	for _, s := range good {
		r, err := ParseRef(s)
		if err != nil || r.String() != s {
			t.Errorf("ParseRef(%q) = %v, %v", s, r, err)
		}
	}
	bad := []string{
		"", "jira", "jira:", "jira:abc-1", "github:acme/shop", "github:acme/shop#0",
		"github:acme/shop#1 x", "advisory:GHSA-CFGH-2345-wxvr@acme/app", "advisory:GHSA-abcd-2345@acme/app",
		"advisory:GHSA-cfgh-2345-wxvr", "advisory:CVE-2024-12345@", "advisory:CVE-2024-12345@acme", "advisory:CVE-2024-12345@acme/a b",
		"svn:acme/x#1", "branch:acme/shop:a\nb", "sentry:acme#1",
	}
	for _, s := range bad {
		if _, err := ParseRef(s); err == nil {
			t.Errorf("ParseRef(%q) accepted", s)
		}
	}
}

func TestIdempotencyKey(t *testing.T) {
	if IdempotencyKey("a", "b") != IdempotencyKey("a", "b") { //nolint:staticcheck // SA4000: calling twice is the point
		t.Error("not stable")
	}
	if IdempotencyKey("ab", "c") == IdempotencyKey("a", "bc") {
		t.Error("parts run together")
	}
}

func TestEnvelopeValidate(t *testing.T) {
	e := Envelope{Ref: "jira:ABC-1", Kind: "x", Actor: "a", Key: "k", Payload: json.RawMessage(`{}`)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.Actor = ""
	if e.Validate() == nil {
		t.Error("empty actor accepted")
	}
}
