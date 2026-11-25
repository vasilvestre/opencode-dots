package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCorrelateSessions(t *testing.T) {
	t0 := time.UnixMilli(1_000_000_000_000)
	steps := []stepWindow{
		{Name: "review", Start: t0, End: t0.Add(10 * time.Minute)},
		{Name: "later", Start: t0.Add(20 * time.Minute), End: t0.Add(30 * time.Minute)},
	}
	rows := []sessionRow{
		{ID: "a", Model: `{"id":"m","providerID":"p"}`, Created: t0.Add(time.Second).UnixMilli(), Updated: 10, Idle: 10, Tools: 3, Out: 7},
		{ID: "b", Created: t0.Add(2 * time.Second).UnixMilli(), Updated: 10, Idle: 0, Tools: 641},
		{ID: "c", Created: t0.Add(21 * time.Minute).UnixMilli(), Updated: 10, Idle: 0},
		{ID: "d", Created: t0.Add(time.Hour).UnixMilli()},
	}
	got := correlateSessions(steps, rows)
	if len(got) != 3 {
		t.Fatalf("want 3 sessions, got %+v", got)
	}
	if a := got[0]; a.Step != "review" || a.Model != "p/m" || a.Orphan || a.Loop || a.Tools != 3 || a.Output != 7 {
		t.Errorf("a: %+v", a)
	}
	if b := got[1]; !b.Orphan || !b.Loop {
		t.Errorf("b should be orphan and loop: %+v", b)
	}
	if c := got[2]; c.Step != "later" || !c.Orphan {
		t.Errorf("c is idle-less but its step is over: orphan: %+v", c)
	}
}

func TestQuerySessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "o.db")
	sql := `CREATE TABLE session_v2 (id text, directory text, model text, tokens_output integer, time_created integer, time_updated integer, time_idle integer);
CREATE TABLE session_part (session_id text, data text);
INSERT INTO session_v2 VALUES ('in','/s/x','{"id":"m"}',5,100,200,NULL),('other','/elsewhere',NULL,0,100,200,NULL),('old','/s',NULL,0,1,2,3);
INSERT INTO session_part VALUES ('in','{"type":"tool"}'),('in','{"type":"text"}'),('in','{"type":"tool"}');`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	rows, err := querySessions(context.Background(), db, "/s", 50, 300)
	if err != nil || len(rows) != 1 || rows[0].ID != "in" || rows[0].Tools != 2 || rows[0].Idle != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if _, err := querySessions(context.Background(), filepath.Join(t.TempDir(), "none.db"), "/s", 0, 1); err == nil {
		t.Error("a missing database should be an error")
	}
}
