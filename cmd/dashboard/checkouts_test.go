package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkoutRig has a fake `where` (the machine map) and a fake `gh`, and a temporary home.
type checkoutRig struct {
	*rig
	home, ghLog string
}

func newCheckoutRig(t *testing.T) *checkoutRig {
	r := newRig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(r.d.root, "bin"), 0o755)
	// where: a folder is a clone when it holds a .clone marker (what the fake gh writes); every project is
	// suggested under ~/acme.
	where := `#!/bin/sh
if [ "$1" = --suggest ]; then echo "` + home + `/acme/${2#*/}"; exit 0; fi
[ -e "` + home + `/acme/${1#*/}/.clone" ] && { echo "` + home + `/acme/${1#*/}"; exit 0; }
exit 1
`
	os.WriteFile(filepath.Join(r.d.root, "bin", "where"), []byte(where), 0o755)
	ghLog := filepath.Join(home, "gh.log")
	gh := filepath.Join(home, "gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho \"$*\" >> "+ghLog+"\n[ -z \"$FAKE_GH_FAIL\" ] || { echo 'authentication required'; exit 1; }\nmkdir -p \"$4\" && touch \"$4/.clone\"\n"), 0o755)
	r.d.gh = gh
	os.MkdirAll(filepath.Join(home, ".config", "dots"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "dots", "served"), []byte("# served\nacme/webapp\nother/tool\n"), 0o644)
	os.MkdirAll(filepath.Join(home, "acme", "app"), 0o755)
	os.WriteFile(filepath.Join(home, "acme", "app", ".clone"), nil, 0o644)
	return &checkoutRig{rig: r, home: home, ghLog: ghLog}
}

func (c *checkoutRig) rows() map[string]checkoutRow {
	var out struct{ Projects []checkoutRow }
	json.Unmarshal(c.owner("/api/checkouts").Body.Bytes(), &out)
	m := map[string]checkoutRow{}
	for _, p := range out.Projects {
		m[p.Repo] = p
	}
	return m
}

func (c *checkoutRig) clone(repo string) int {
	return ask(c.h, "POST", "/api/checkouts/clone", "owner", "", `{"repo":"`+repo+`"}`).Code
}

func TestCheckoutsListWhatIsClonedAndWhereTheRestShouldGo(t *testing.T) {
	c := newCheckoutRig(t)
	rows := c.rows()
	if got := rows["acme/app"]; got.State != "found" || got.Path != c.home+"/acme/app" {
		t.Fatalf("acme/app = %+v, want found in the home folder", got)
	}
	web := rows["acme/webapp"]
	if web.State != "missing" || web.Suggested != c.home+"/acme/webapp" {
		t.Fatalf("acme/webapp = %+v, want missing with the suggested folder", web)
	}
	if rows["other/tool"].State != "missing" {
		t.Fatalf("a repository from the served file must be listed: %+v", rows)
	}
}

func TestCheckoutCloneCreatesTheProjectWhereWeSuggested(t *testing.T) {
	c := newCheckoutRig(t)
	if code := c.clone("acme/webapp"); code != 202 {
		t.Fatalf("clone = %d, want 202", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(c.home, "acme", "webapp")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ := os.ReadFile(c.ghLog)
	if !strings.Contains(string(raw), "repo clone acme/webapp "+c.home+"/acme/webapp") {
		t.Fatalf("gh was asked %q, want a clone of acme/webapp into the suggested folder", raw)
	}
	time.Sleep(100 * time.Millisecond)
	if got := c.rows()["acme/webapp"]; got.State != "found" {
		t.Fatalf("after the clone = %+v, want found", got)
	}
}

func TestCheckoutCloneFailureIsShownNotHidden(t *testing.T) {
	c := newCheckoutRig(t)
	t.Setenv("FAKE_GH_FAIL", "1")
	if code := c.clone("acme/webapp"); code != 202 {
		t.Fatalf("clone = %d", code)
	}
	var row checkoutRow
	for i := 0; i < 100; i++ {
		row = c.rows()["acme/webapp"]
		if row.State == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if row.State != "failed" || !strings.Contains(row.Error, "authentication required") {
		t.Fatalf("row = %+v, want failed with gh's message", row)
	}
}

func TestCheckoutCloneRefusesWhatItShould(t *testing.T) {
	c := newCheckoutRig(t)
	c.signIn()
	if rec := ask(c.h, "POST", "/api/checkouts/clone", "visitor", c.cookie, `{"repo":"acme/webapp"}`); rec.Code != 403 {
		t.Errorf("a visitor cloning = %d, want 403", rec.Code)
	}
	if rec := ask(c.h, "GET", "/api/checkouts", "visitor", c.cookie, ""); rec.Code != 403 {
		t.Errorf("a visitor listing = %d, want 403", rec.Code)
	}
	for name, tc := range map[string]struct {
		repo string
		want int
	}{
		"a repository this machine does not serve": {"nobody/else", 404},
		"a bad name":               {"../x", 400},
		"a project already cloned": {"acme/app", 409},
	} {
		if got := c.clone(tc.repo); got != tc.want {
			t.Errorf("%s = %d, want %d", name, got, tc.want)
		}
	}
	// A folder with that name that is not a clone: refused, and gh never runs into it.
	os.MkdirAll(filepath.Join(c.home, "acme", "webapp"), 0o755)
	if got := c.clone("acme/webapp"); got != 409 {
		t.Errorf("an existing folder that is not a clone = %d, want 409", got)
	}
	if raw, _ := os.ReadFile(c.ghLog); strings.Contains(string(raw), "acme/webapp") {
		t.Errorf("gh must not run into a folder that already exists: %q", raw)
	}
}

func TestCheckoutCloneRefusesAFolderOutsideTheHome(t *testing.T) {
	c := newCheckoutRig(t)
	os.WriteFile(filepath.Join(c.d.root, "bin", "where"), []byte("#!/bin/sh\n[ \"$1\" = --suggest ] && { echo /tmp/elsewhere/webapp; exit 0; }\nexit 1\n"), 0o755)
	if got := c.clone("acme/webapp"); got != 422 {
		t.Fatalf("a suggestion outside the home = %d, want 422", got)
	}
	os.WriteFile(filepath.Join(c.d.root, "bin", "where"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if got := c.clone("acme/webapp"); got != 422 {
		t.Fatalf("no suggestion at all = %d, want 422", got)
	}
}
