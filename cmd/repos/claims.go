package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"opencode-dots/internal/events"
	"opencode-dots/internal/store"
)

// exitClaimed is the exit status of `repos db claimed` when a human holds the branch: a
// shell step tells it from a failure (1) and from a free branch (0).
const exitClaimed = 10

type heldError struct{ msg string }

func (e heldError) Error() string { return e.msg }

// dbClaim holds the flags of the claim subcommands (ADR 0026).
type dbClaim struct {
	ID, WorkItem, Ref, Machine string
	Repo, Branch, Actor        string
	Name, Capabilities         string // db worker (ADR 0042)
	TTL                        time.Duration
}

func claimCommand(ctx context.Context, st *store.Store, sub, workspace string, c dbClaim) error {
	switch sub {
	case "claim":
		if c.Actor == "" {
			return errors.New("repos db claim: -actor LOGIN is required")
		}
		id, err := workItemID(ctx, st, c)
		if err != nil {
			return err
		}
		who, err := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: c.Actor, Identities: map[string]string{"github": c.Actor}})
		if err != nil {
			return err
		}
		claim, err := st.TakeClaim(ctx, id, who.ID, c.Machine, c.Branch, c.TTL)
		if err != nil {
			return err
		}
		active, err := st.ActiveClaims(ctx, id)
		if err != nil {
			return err
		}
		if humans := countHumans(active); humans > 1 {
			fmt.Fprintf(os.Stderr, "repos: warning: %d humans hold claims on %s\n", humans, id)
		}
		fmt.Printf("claim=%s work_item=%s expires=%s\n", claim.ID, id, claim.Expires())
		kind := "created"
		if claim.Renewed() {
			kind = "renewed"
		}
		announce(ctx, kind, claim)
		return nil
	case "heartbeat":
		claim, err := st.Heartbeat(ctx, c.ID, c.TTL)
		if errors.Is(err, store.ErrExpired) {
			return fmt.Errorf("claim %s is no longer active: claim again (the bot may have worked in between)", c.ID)
		}
		if err != nil {
			return err
		}
		fmt.Printf("claim=%s expires=%s\n", claim.ID, claim.Expires())
		announce(ctx, "renewed", claim)
		return nil
	case "release":
		claim, err := st.ReleaseClaim(ctx, c.ID)
		if err == nil {
			announce(ctx, "released", claim)
		}
		return err
	case "claims":
		active, err := st.ActiveClaims(ctx, c.WorkItem)
		if err != nil {
			return err
		}
		for _, a := range active {
			fmt.Printf("%s\t%s\t%s\t%s\texpires %s\n", a.ID, a.ActorKind, a.Machine, a.Branch, a.Expires())
		}
		return nil
	default: // claimed
		if c.Repo == "" || c.Branch == "" {
			return errors.New("repos db claimed: -repo and -branch are required")
		}
		held, ok, err := st.HumanClaimOnBranch(ctx, c.Repo, c.Branch)
		if err != nil {
			return err
		}
		if ok {
			return heldError{fmt.Sprintf("held by claim %s (machine %s) until %s", held.ID, held.Machine, held.Expires())}
		}
		return nil
	}
}

// announce tells the hub that a claim changed, when DOTS_MERCURE_URL and DOTS_MERCURE_SECRET are
// set. It is best effort: the claim is in the database, and a hub that is down loses a
// notification, not a claim (ADR 0026).
func announce(ctx context.Context, kind string, c store.Claim) {
	hub, ok := hubFromEnv()
	if !ok {
		return
	}
	payload, _ := json.Marshal(events.ClaimUpdate{Kind: kind, ClaimID: c.ID, WorkItemID: c.WorkItemID, ActorID: c.ActorID, Machine: c.Machine, Branch: c.Branch})
	if err := hub.Publish(ctx, events.ClaimTopic(c.WorkItemID), payload, true); err != nil {
		fmt.Fprintln(os.Stderr, "repos: warning: the claim is saved but the hub was not told:", err)
	}
}

func hubFromEnv() (events.Hub, bool) {
	url, secret := os.Getenv("DOTS_MERCURE_URL"), os.Getenv("DOTS_MERCURE_SECRET")
	issuer := os.Getenv("DOTS_MERCURE_ISSUER")
	if issuer == "" {
		issuer = "dots"
	}
	return events.Hub{URL: url, Secret: []byte(secret), Issuer: issuer,
		Match: os.Getenv("DOTS_MERCURE_MATCH"), MatchType: os.Getenv("DOTS_MERCURE_MATCH_TYPE")}, url != "" && secret != ""
}

func workItemID(ctx context.Context, st *store.Store, c dbClaim) (string, error) {
	switch {
	case c.WorkItem != "":
		return c.WorkItem, nil
	case c.Ref != "":
		w, err := st.WorkItemByRef(ctx, c.Ref)
		if err != nil {
			return "", fmt.Errorf("no work item holds %s (observe the event first): %w", c.Ref, err)
		}
		return w.ID, nil
	}
	return "", errors.New("repos db claim: -work-item ID or -ref REF is required")
}

func countHumans(claims []store.Claim) int {
	n := 0
	for _, c := range claims {
		if c.ActorKind == "human" {
			n++
		}
	}
	return n
}
