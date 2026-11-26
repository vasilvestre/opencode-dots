package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"opencode-dots/internal/store"
)

// dbWorker holds the flags of the worker subcommands (ADR 0042, first step: presence only).
type dbWorker struct {
	Name, Machine, Capabilities string
	TTL                         time.Duration
}

// workerCommand runs `repos db worker register|heartbeat|list|expire`. Every time is the
// server's clock; the worker never sends one.
func workerCommand(ctx context.Context, st *store.Store, out io.Writer, action string, w dbWorker) error {
	switch action {
	case "register":
		if w.Name == "" || w.Machine == "" {
			return errors.New("repos db worker register: -name and -machine are required")
		}
		got, err := st.RegisterWorker(ctx, w.Name, w.Machine, []byte(w.Capabilities), w.TTL)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "worker=%s expires=%s\n", got.Name, got.ExpiresAt.UTC().Format(time.RFC3339))
		return nil
	case "heartbeat":
		if w.Name == "" {
			return errors.New("repos db worker heartbeat: -name is required")
		}
		got, err := st.HeartbeatWorker(ctx, w.Name, w.TTL)
		if errors.Is(err, store.ErrExpired) {
			return fmt.Errorf("worker %s is no longer registered: register again", w.Name)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "worker=%s expires=%s\n", got.Name, got.ExpiresAt.UTC().Format(time.RFC3339))
		return nil
	case "expire":
		if w.Name == "" {
			return errors.New("repos db worker expire: -name is required")
		}
		got, err := st.ExpireWorker(ctx, w.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "worker=%s expired\n", got.Name)
		return nil
	case "list":
		list, err := st.Workers(ctx)
		if err != nil {
			return err
		}
		now := st.Now()
		for _, x := range list {
			state := "expired"
			if x.Live(now) {
				state = "live"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\texpires %s\t%s\n", x.Name, x.Machine, state, x.ExpiresAt.UTC().Format(time.RFC3339), x.Capabilities)
		}
		return nil
	}
	return fmt.Errorf("repos db worker: unknown action %q (register, heartbeat, list, expire)", action)
}
