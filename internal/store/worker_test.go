package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func workerStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := open(t)
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return now }
	return s, &now
}

func TestAWorkerExpiresWhenTheHeartbeatsStop(t *testing.T) {
	s, now := workerStore(t)
	ctx := context.Background()
	w, err := s.RegisterWorker(ctx, "laptop-ada", "ada-laptop", []byte(`{"models":["local"]}`), 0)
	if err != nil || w.ExpiresAt.Sub(*now) != DefaultWorkerTTL || string(w.Capabilities) != `{"models":["local"]}` {
		t.Fatalf("worker = %+v, %v", w, err)
	}
	a, err := s.actorByIdentity(ctx, "worker", "laptop-ada")
	if err != nil || a.Kind != "bot" || a.ID != w.ActorID {
		t.Errorf("actor = %+v, %v", a, err)
	}
	*now = now.Add(60 * time.Second)
	if w, err = s.HeartbeatWorker(ctx, "laptop-ada", 0); err != nil || w.ExpiresAt.Sub(*now) != DefaultWorkerTTL {
		t.Fatalf("heartbeat = %+v, %v", w, err)
	}
	*now = now.Add(DefaultWorkerTTL + time.Second)
	list, _ := s.Workers(ctx)
	if len(list) != 1 || list[0].Live(s.Now()) {
		t.Errorf("workers = %+v", list)
	}
	if _, err := s.HeartbeatWorker(ctx, "laptop-ada", 0); !errors.Is(err, ErrExpired) {
		t.Errorf("a late heartbeat revived the worker: %v", err)
	}
	if _, err := s.HeartbeatWorker(ctx, "nobody", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worker: %v", err)
	}
}

func TestRegisteringTwiceIsOneWorkerAndAReregistrationRevives(t *testing.T) {
	s, now := workerStore(t)
	ctx := context.Background()
	first, _ := s.RegisterWorker(ctx, "w", "m", nil, time.Hour*5) // capped
	if first.ExpiresAt.Sub(*now) != MaxWorkerTTL || string(first.Capabilities) != "{}" {
		t.Errorf("first = %+v", first)
	}
	*now = now.Add(time.Minute)
	again, _ := s.RegisterWorker(ctx, "w", "m2", []byte(`{}`), 0)
	if !again.RegisteredAt.Equal(first.RegisteredAt) || again.Machine != "m2" {
		t.Errorf("a live re-registration changed the registration time: %+v", again)
	}
	*now = now.Add(MaxWorkerTTL + time.Minute)
	revived, _ := s.RegisterWorker(ctx, "w", "m2", nil, 0)
	if !revived.RegisteredAt.Equal(*now) || !revived.Live(*now) {
		t.Errorf("revived = %+v", revived)
	}
	if list, _ := s.Workers(ctx); len(list) != 1 {
		t.Errorf("workers = %d, want 1", len(list))
	}
}

func TestExpireEndsAWorkerNow(t *testing.T) {
	s, now := workerStore(t)
	ctx := context.Background()
	s.RegisterWorker(ctx, "w", "m", nil, 0)
	w, err := s.ExpireWorker(ctx, "w")
	if err != nil || !w.ExpiresAt.Equal(*now) || w.Live(*now) {
		t.Fatalf("expired = %+v, %v", w, err)
	}
	if _, err := s.ExpireWorker(ctx, "w"); err != nil {
		t.Errorf("expiring twice: %v", err)
	}
	if _, err := s.ExpireWorker(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown worker: %v", err)
	}
}

func TestAWorkerIsChecked(t *testing.T) {
	s, _ := workerStore(t)
	ctx := context.Background()
	for _, c := range []struct{ name, machine, caps string }{
		{"", "m", ""}, {"a b", "m", ""}, {"w", " ", ""}, {"w", "m", "[1]"}, {"w", "m", "nope"}, {"w", "m", "null"},
	} {
		if _, err := s.RegisterWorker(ctx, c.name, c.machine, []byte(c.caps), 0); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}
