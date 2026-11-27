package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dailyRuns adds the runs of the daily pass to a rig, under the same state as fixture:
//   - tend-open and tend-hidden name their repository in the opening sentence of the prompt;
//   - input-open has a bare log and the repository only in awf's recorded inputs;
//   - mention-only quotes both repositories in free text and carries no label.
func dailyRuns(t *testing.T, r *rig) {
	t.Helper()
	state := r.d.state
	put := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cost := func(usd string) string {
		return `data: {"kind":"step.completed","cost_usd":` + usd + `}` + "\n"
	}
	prompt := func(text string) string {
		b, _ := json.Marshal(map[string]string{"kind": "message.user", "text": text})
		return "data: " + string(b) + "\n"
	}
	at := r.clk.now().Add(-time.Hour).Format(time.RFC3339)
	idx := ""
	add := func(id, log string) {
		put(filepath.Join(state, "logs", id+".jsonl"), log)
		idx += `{"execution_id":"` + id + `","workflow_name":"tend-repo","started_at":"` + at + `"}` + "\n"
	}
	add("tend-open-001", prompt("You keep one repository honest. It is acme/app, mostly written by\nAI agents.\n- acme/client#9 was merged")+cost("0.01"))
	add("tend-hidden-1", prompt("You keep one repository honest. It is acme/client, mostly written by\nAI agents.")+cost("0.01"))
	add("mention-only1", prompt("Please summarise. A pull request said: you keep acme/client honest, see acme/app and\nrepo: acme/client")+cost("0.2"))
	add("late-label-01", prompt("Hello")+prompt(strings.Repeat("x", 10)+"\nrepo: acme/client\n")+cost("0.3"))
	add("input-open-01", "data: {\"kind\":\"step.started\"}\n")
	put(filepath.Join(state, "awf", "transcripts", "input-open-01.jsonl"), cost("0.02"))
	put(filepath.Join(state, "awf", "states", "input-open-01.json"), `{"WorkflowName":"tend-repo","Inputs":{"repo":"acme/app"}}`)
	f, err := os.OpenFile(filepath.Join(state, "logs", "index.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(idx)
	r.d.clock = r.clk.now
}

type usageAnswer struct {
	Total  float64    `json:"total"`
	Runs   int        `json:"runs"`
	Groups []usageOne `json:"groups"`
}

func usageOf(t *testing.T, body []byte) (usageAnswer, map[string]usageOne) {
	t.Helper()
	var u usageAnswer
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	by := map[string]usageOne{}
	for _, g := range u.Groups {
		by[g.Key] = g
	}
	return u, by
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestDailyPassRunsAreAttributedToTheirRepository(t *testing.T) {
	r := newRig(t)
	r.signIn()
	dailyRuns(t, r)

	u, by := usageOf(t, r.owner("/api/usage").Body.Bytes())
	// Fixture runs 0.5 + 0.5, then 0.01 + 0.01 + 0.2 + 0.3 + 0.02: the total is what it was
	// before the attribution; only the grouping moved.
	if u.Runs != 7 || !near(u.Total, 1.54) {
		t.Fatalf("owner totals = %d runs, %v", u.Runs, u.Total)
	}
	if g := by["acme/app"]; g.Repo != "acme/app" || g.Runs != 2 || !near(g.Cost, 0.03) {
		t.Fatalf("acme/app group = %+v", g)
	}
	if g := by["acme/client"]; g.Repo != "acme/client" || g.Runs != 1 || !near(g.Cost, 0.01) {
		t.Fatalf("acme/client group = %+v", g)
	}
	// Free text and a label past the first user message name no repository.
	if g := by["(no pull request)"]; g.Repo != "" || g.Runs != 2 || !near(g.Cost, 0.5) {
		t.Fatalf("unattributed group = %+v", g)
	}

	v, vby := usageOf(t, r.visitor("/api/usage").Body.Bytes())
	if v.Runs != 3 || !near(v.Total, 0.53) {
		t.Fatalf("visitor totals = %d runs, %v: %s", v.Runs, v.Total, r.visitor("/api/usage").Body)
	}
	if g := vby["acme/app"]; g.Runs != 2 || !near(g.Cost, 0.03) {
		t.Fatalf("visitor sees %+v under acme/app", g)
	}
	for key := range vby {
		if strings.Contains(key, "client") || key == "(no pull request)" {
			t.Fatalf("visitor sees the group %q", key)
		}
	}
}

func TestHomeListsWhatTheViewerMaySee(t *testing.T) {
	r := newRig(t)
	r.signIn()
	dailyRuns(t, r)

	var owner []homeRepo
	if err := json.Unmarshal(r.owner("/api/home").Body.Bytes(), &owner); err != nil {
		t.Fatal(err)
	}
	by := map[string]homeRepo{}
	for _, x := range owner {
		by[x.Repo] = x
	}
	app, client := by["acme/app"], by["acme/client"]
	// Fixture review run + tend run + input run in the last 7 days.
	if app.Runs7d != 3 || !near(app.Cost7d, 0.53) || app.Notes != 1 || app.Held != 0 || !app.HasMemory || app.LastAt == "" {
		t.Fatalf("acme/app row = %+v", app)
	}
	if client.Runs7d != 2 || client.Held != 1 || client.Notes != 0 || client.HasMemory != true {
		t.Fatalf("acme/client row = %+v", client)
	}

	res := r.visitor("/api/home")
	if res.Code != 200 {
		t.Fatalf("visitor home = %d %s", res.Code, res.Body)
	}
	var visitor []homeRepo
	if err := json.Unmarshal(res.Body.Bytes(), &visitor); err != nil {
		t.Fatal(err)
	}
	if len(visitor) != 1 || visitor[0] != app {
		t.Fatalf("visitor rows = %+v, want only %+v", visitor, app)
	}
	// Nothing about the hidden repository, not even its name or a count, is in the answer.
	if strings.Contains(res.Body.String(), "client") {
		t.Fatalf("the answer names the hidden repository: %s", res.Body)
	}
}

func TestHomeAddsARepositoryOfTheSessionTheDotKnowsNothingAbout(t *testing.T) {
	r := newRig(t)
	r.gh.perms["acme/new"] = "write"
	// The dot's own list does not name it, so the visitor's row is empty.
	if err := os.WriteFile(filepath.Join(r.d.root, "awf-gh.yaml"), []byte("repos:\n  - repo: acme/app\n    scope: s\n    workflow: review-pr\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.d.state, "tend")); err != nil {
		t.Fatal(err)
	}
	r.signIn()
	var rows []homeRepo
	json.Unmarshal(r.visitor("/api/home").Body.Bytes(), &rows)
	got := []string{}
	for _, x := range rows {
		got = append(got, x.Repo)
	}
	if strings.Join(got, ",") == "" || len(got) < 1 {
		t.Fatalf("no rows: %v", got)
	}
	for _, x := range got {
		if x == "acme/client" {
			t.Fatalf("hidden row in %v", got)
		}
	}
}

func TestHomeNeedsASessionForAVisitor(t *testing.T) {
	r := newRig(t)
	if res := r.visitor("/api/home"); res.Code != 401 {
		t.Fatalf("home without a session = %d", res.Code)
	}
}
