package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedHost(t *testing.T) {
	d := &dash{allow: map[string]bool{"machine.example.ts.net": true}}
	req := func(host, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/save", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	ok := []struct{ host, origin string }{
		{"127.0.0.1:8099", ""},
		{"machine.example.ts.net", ""},
		{"machine.example.ts.net", "https://machine.example.ts.net"},
		{"127.0.0.1:8099", "https://machine.example.ts.net"},
	}
	for _, c := range ok {
		if !d.trustedRequest(req(c.host, c.origin)) {
			t.Errorf("host %q origin %q: refused, want allowed", c.host, c.origin)
		}
	}
	bad := []struct{ host, origin string }{
		{"evil.example", ""},
		{"machine.example.ts.net", "https://evil.example"},
		{"evil.example", "https://machine.example.ts.net"},
		{"machine.example.ts.net.evil.example", ""},
	}
	for _, c := range bad {
		if d.trustedRequest(req(c.host, c.origin)) {
			t.Errorf("host %q origin %q: allowed, want refused", c.host, c.origin)
		}
	}
}
