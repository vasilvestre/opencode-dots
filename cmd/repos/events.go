package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// eventsCommand talks to the Mercure hub named by DOTS_MERCURE_URL and DOTS_MERCURE_SECRET:
// `publish TOPIC DATA` sends one private update, `subscribe TOPIC...` prints each update's
// data, one per line, until interrupted. A local agent follows a work item's claims with it.
func eventsCommand(sub string, args []string) error {
	hub, ok := hubFromEnv()
	if !ok {
		return errors.New("repos events: set DOTS_MERCURE_URL and DOTS_MERCURE_SECRET")
	}
	switch sub {
	case "publish":
		if len(args) != 2 {
			return errors.New("repos events publish: want TOPIC DATA")
		}
		return hub.Publish(context.Background(), args[0], []byte(args[1]), true)
	case "subscribe":
		if len(args) == 0 {
			return errors.New("repos events subscribe: want at least one TOPIC")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		for ev := range hub.Subscribe(ctx, args...) {
			fmt.Println(ev.Data)
		}
		return nil
	default:
		return fmt.Errorf("repos events: unknown subcommand %q (publish, subscribe)", sub)
	}
}
