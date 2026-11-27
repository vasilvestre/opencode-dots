package workertokens

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIssueIsBounded(t *testing.T) {
	s := New("")
	for name, c := range map[string]struct {
		label string
		ttl   time.Duration
	}{"no label": {"", 0}, "long label": {strings.Repeat("a", 61), 0}, "over 90 days": {"x", 91 * 24 * time.Hour}} {
		if _, _, err := s.Issue(c.label, "", nil, c.ttl); err == nil {
			t.Errorf("%s was issued", name)
		}
	}
	secret, tok, err := s.Issue("laptop", "alice", nil, 0)
	if err != nil || !strings.HasPrefix(secret, Prefix) || tok.Expires.Sub(tok.Created) != defaultTTL {
		t.Fatalf("issue = %q %+v %v", secret, tok, err)
	}
}

func TestRevokedAndExpiredTokensStop(t *testing.T) {
	s := New("")
	secret, tok, _ := s.Issue("a", "", nil, 24*time.Hour)
	if _, ok := s.Lookup(secret); !ok {
		t.Fatal("a fresh token is refused")
	}
	if !s.Revoke(tok.ID) || s.Revoke(tok.ID) {
		t.Fatal("revoke must work once")
	}
	if _, ok := s.Lookup(secret); ok {
		t.Fatal("a revoked token still works")
	}
	s2, _, _ := s.Issue("b", "", nil, 24*time.Hour)
	s.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, ok := s.Lookup(s2); ok {
		t.Fatal("an expired token still works")
	}
}

func TestAllowed(t *testing.T) {
	known := []string{"Acme/App", "acme/lib"}
	if got := (Token{}).Allowed(known); !slices.Equal(got, known) {
		t.Fatalf("no list = %v", got)
	}
	if got := (Token{Repos: []string{"acme/app"}}).Allowed(known); !slices.Equal(got, []string{"Acme/App"}) {
		t.Fatalf("listed = %v", got)
	}
}
