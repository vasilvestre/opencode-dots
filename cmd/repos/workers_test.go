package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

func TestWorkerCommands(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := func(action string, w dbWorker) (string, error) {
		var out bytes.Buffer
		err := workerCommand(ctx, st, &out, action, w)
		return out.String(), err
	}

	if _, err := run("register", dbWorker{Name: "w1"}); err == nil {
		t.Error("register without a machine was accepted")
	}
	if _, err := run("register", dbWorker{Name: "w1", Machine: "laptop", Capabilities: "[1]"}); err == nil {
		t.Error("register with capabilities that are not an object was accepted")
	}
	if out, err := run("register", dbWorker{Name: "w1", Machine: "laptop", Capabilities: `{"gpu":true}`}); err != nil || !strings.HasPrefix(out, "worker=w1 expires=") {
		t.Fatalf("register = %q, %v", out, err)
	}
	if _, err := run("heartbeat", dbWorker{Name: "w1"}); err != nil {
		t.Errorf("heartbeat: %v", err)
	}
	if out, _ := run("list", dbWorker{}); !strings.Contains(out, "w1\tlaptop\tlive") || !strings.Contains(out, `{"gpu":true}`) {
		t.Errorf("list = %q", out)
	}
	if _, err := run("expire", dbWorker{Name: "w1"}); err != nil {
		t.Errorf("expire: %v", err)
	}
	if out, _ := run("list", dbWorker{}); !strings.Contains(out, "w1\tlaptop\texpired") {
		t.Errorf("list after expire = %q", out)
	}
	if _, err := run("heartbeat", dbWorker{Name: "w1"}); err == nil || !strings.Contains(err.Error(), "register again") {
		t.Errorf("heartbeat of an expired worker: %v", err)
	}
	if _, err := run("bogus", dbWorker{}); err == nil {
		t.Error("an unknown action was accepted")
	}
}
