package main

import (
	"errors"
	"testing"
	"time"
)

func probes(failing map[string]error) func(string) error {
	return func(u string) error { return failing[u] }
}

func token(valid bool, exp time.Time, err error) func() (bool, time.Time, error) {
	return func() (bool, time.Time, error) { return valid, exp, err }
}

func units(avail bool, state map[string]string) (func() bool, func(string) (string, bool)) {
	return func() bool { return avail }, func(u string) (string, bool) { s, ok := state[u]; return s, ok }
}

func disk(werr error, free uint64) (func(string) error, func(string) (uint64, error)) {
	return func(string) error { return werr }, func(string) (uint64, error) { return free, nil }
}

func runs(t time.Time, found bool) func(string) (time.Time, bool, error) {
	return func(string) (time.Time, bool, error) { return t, found, nil }
}

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func good() (Config, Deps) {
	c := Config{AwfURL: "http://awf", Executors: map[string]string{"ws": "http://ws"}, StateDir: "/s",
		Units: []string{"a.service"}, MinFree: 1 << 30, WarnDays: 7, StaleAfter: 48 * time.Hour, Now: now, HaveGHToken: true}
	d := Deps{Probe: probes(nil), Token: token(true, now.Add(30*24*time.Hour), nil), LatestRun: runs(now.Add(-time.Hour), true)}
	d.UnitsAvailable, d.UnitState = units(true, map[string]string{"a.service": "active"})
	d.Writable, d.FreeBytes = disk(nil, 5<<30)
	return c, d
}

func TestHealthy(t *testing.T) {
	c, d := good()
	if r := Run(c, d); r.ExitCode() != 0 {
		t.Fatalf("got %s\n%s", r.Overall, r.Text())
	}
}

func TestDegradedAndDown(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Config, *Deps)
		want int
	}{
		{"executor down", func(c *Config, d *Deps) {
			d.Probe = probes(map[string]error{"http://ws/api/workflows": errors.New("refused")})
		}, 1},
		{"awf down", func(c *Config, d *Deps) {
			d.Probe = probes(map[string]error{"http://awf/api/workflows": errors.New("refused")})
		}, 2},
		{"token expiring", func(c *Config, d *Deps) { d.Token = token(true, now.Add(3*24*time.Hour), nil) }, 1},
		{"token revoked", func(c *Config, d *Deps) { d.Token = token(false, time.Time{}, nil) }, 2},
		{"token expired", func(c *Config, d *Deps) { d.Token = token(true, now.Add(-time.Hour), nil) }, 2},
		{"github unreachable", func(c *Config, d *Deps) { d.Token = token(false, time.Time{}, errors.New("dns")) }, 1},
		{"no token", func(c *Config, d *Deps) { c.HaveGHToken = false }, 2},
		{"unit inactive", func(c *Config, d *Deps) {
			d.UnitsAvailable, d.UnitState = units(true, map[string]string{"a.service": "failed"})
		}, 1},
		{"no systemd", func(c *Config, d *Deps) { d.UnitsAvailable, d.UnitState = units(false, nil) }, 0},
		{"state read-only", func(c *Config, d *Deps) { d.Writable, d.FreeBytes = disk(errors.New("ro"), 5<<30) }, 2},
		{"disk low", func(c *Config, d *Deps) { d.Writable, d.FreeBytes = disk(nil, 1<<20) }, 2},
		{"stale run", func(c *Config, d *Deps) { d.LatestRun = runs(now.Add(-72*time.Hour), true) }, 1},
		{"no run", func(c *Config, d *Deps) { d.LatestRun = runs(time.Time{}, false) }, 1},
	}
	for _, tc := range cases {
		c, d := good()
		tc.mod(&c, &d)
		if got := Run(c, d).ExitCode(); got != tc.want {
			t.Errorf("%s: exit %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	if got := parseExpiry("2026-12-01 10:00:00 UTC"); got.Day() != 1 || got.Month() != 12 {
		t.Fatalf("got %v", got)
	}
	if !parseExpiry("").IsZero() || !parseExpiry("junk").IsZero() {
		t.Fatal("want zero")
	}
}
