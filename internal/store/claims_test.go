package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// claimFixture is a store with one project, a human, a bot and a work item that holds a
// branch reference, and a clock the test moves.
type claimFixture struct {
	s     *Store
	ctx   context.Context
	human Actor
	other Actor
	bot   Actor
	item  WorkItem
	now   time.Time
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	f := &claimFixture{s: open(t), ctx: context.Background(), now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	f.s.clock = func() time.Time { return f.now }
	f.human, _ = f.s.EnsureActor(f.ctx, Actor{Kind: "human", DisplayName: "Ada", Identities: map[string]string{"github": "ada"}})
	f.other, _ = f.s.EnsureActor(f.ctx, Actor{Kind: "human", DisplayName: "Grace", Identities: map[string]string{"github": "grace"}})
	f.bot, _ = f.s.EnsureActor(f.ctx, Actor{Kind: "bot", DisplayName: "dots", Identities: map[string]string{"github": "dots[bot]"}})
	p, _, _ := f.s.UpsertProject(f.ctx, "default", "acme/app", 0, nil)
	var err error
	if f.item, err = f.s.CreateWorkItem(f.ctx, p.ID, "Fix", f.human.ID, "github:acme/app#7", "branch:acme/app:fix-login"); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAClaimExpiresWhenTheHeartbeatsStop(t *testing.T) {
	f := newClaimFixture(t)
	c, err := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0)
	if err != nil || c.ExpiresAt.Sub(f.now) != DefaultClaimTTL || c.ActorKind != "human" {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if got, _ := f.s.ActiveClaims(f.ctx, f.item.ID); len(got) != 1 {
		t.Fatalf("active claims = %d, want 1", len(got))
	}
	f.now = f.now.Add(60 * time.Second)
	c, err = f.s.Heartbeat(f.ctx, c.ID, 0)
	if err != nil || c.ExpiresAt.Sub(f.now) != DefaultClaimTTL {
		t.Fatalf("heartbeat = %+v, %v", c, err)
	}
	f.now = f.now.Add(DefaultClaimTTL + time.Second) // the machine went away
	if got, _ := f.s.ActiveClaims(f.ctx, f.item.ID); len(got) != 0 {
		t.Errorf("an expired claim is still active: %v", got)
	}
	if _, err := f.s.Heartbeat(f.ctx, c.ID, 0); !errors.Is(err, ErrExpired) {
		t.Errorf("a late heartbeat revived the claim: %v", err)
	}
}

func TestAskingTwiceFromOneMachineIsOneClaim(t *testing.T) {
	f := newClaimFixture(t)
	a, _ := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0)
	f.now = f.now.Add(10 * time.Second)
	b, err := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "fix-login", 0)
	if err != nil || a.ID != b.ID || !b.ExpiresAt.After(a.ExpiresAt) || b.Branch != "fix-login" {
		t.Errorf("second take = %+v, %v (first %+v)", b, err, a)
	}
}

func TestTwoHumansMayClaimTheSameWorkItem(t *testing.T) {
	f := newClaimFixture(t)
	f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0)
	if _, err := f.s.TakeClaim(f.ctx, f.item.ID, f.other.ID, "desktop", "", 0); err != nil {
		t.Fatalf("a second human claim was refused: %v", err)
	}
	got, _ := f.s.ActiveClaims(f.ctx, f.item.ID)
	humans := 0
	for _, c := range got {
		if c.ActorKind == "human" {
			humans++
		}
	}
	if humans != 2 {
		t.Errorf("active human claims = %d, want both so the caller can warn", humans)
	}
}

func TestReleaseEndsAClaimAndCanBeRepeated(t *testing.T) {
	f := newClaimFixture(t)
	c, _ := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0)
	if released, err := f.s.ReleaseClaim(f.ctx, c.ID); err != nil || released.ReleasedAt.IsZero() {
		t.Fatalf("release = %+v, %v", released, err)
	}
	if _, err := f.s.ReleaseClaim(f.ctx, c.ID); err != nil {
		t.Errorf("releasing twice: %v", err)
	}
	if _, err := f.s.ReleaseClaim(f.ctx, "cl_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("releasing an unknown claim: %v", err)
	}
	if got, _ := f.s.ActiveClaims(f.ctx, f.item.ID); len(got) != 0 {
		t.Error("a released claim is still active")
	}
	if _, err := f.s.Heartbeat(f.ctx, c.ID, 0); !errors.Is(err, ErrExpired) {
		t.Errorf("heartbeat after release: %v", err)
	}
	// The machine may claim again afterwards: the unique rule only covers live claims.
	if _, err := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0); err != nil {
		t.Errorf("claiming again after a release: %v", err)
	}
}

func TestTheTimeToLiveIsBounded(t *testing.T) {
	f := newClaimFixture(t)
	c, _ := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 1000*time.Hour)
	if c.ExpiresAt.Sub(f.now) != MaxClaimTTL {
		t.Errorf("ttl = %v, want it capped at %v", c.ExpiresAt.Sub(f.now), MaxClaimTTL)
	}
}

func TestAClaimNamesItsMachine(t *testing.T) {
	f := newClaimFixture(t)
	if _, err := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "  ", "", 0); err == nil {
		t.Error("a claim without a machine was accepted")
	}
}

func TestTheBotChecksWhetherAHumanHoldsABranch(t *testing.T) {
	f := newClaimFixture(t)
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "fix-login"); held {
		t.Fatal("a branch with no claim reads as held")
	}
	// A claim on the work item covers its branch reference, though it names no branch.
	c, _ := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "", 0)
	got, held, err := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "fix-login")
	if err != nil || !held || got.ID != c.ID {
		t.Fatalf("branch claim = %+v held=%v err=%v", got, held, err)
	}
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "another-branch"); held {
		t.Error("another branch of the repository reads as held: the claim is for one work item")
	}
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/other", "fix-login"); held {
		t.Error("the same branch name in another repository reads as held")
	}
	f.now = f.now.Add(DefaultClaimTTL + time.Second)
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "fix-login"); held {
		t.Error("an expired claim still holds the branch")
	}
}

func TestABotClaimDoesNotVetoTheBot(t *testing.T) {
	f := newClaimFixture(t)
	f.s.TakeClaim(f.ctx, f.item.ID, f.bot.ID, "server", "", 0)
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "fix-login"); held {
		t.Error("the bot's own claim reads as a human's")
	}
}

func TestAClaimNamingABranchHoldsItWithoutAReference(t *testing.T) {
	f := newClaimFixture(t)
	p, _, _ := f.s.UpsertProject(f.ctx, "default", "acme/lib", 1, nil)
	w, _ := f.s.CreateWorkItem(f.ctx, p.ID, "x", f.human.ID)
	f.s.TakeClaim(f.ctx, w.ID, f.human.ID, "laptop", "topic", 0)
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/lib", "topic"); !held {
		t.Error("a claim that names its branch does not hold it")
	}
	if _, held, _ := f.s.HumanClaimOnBranch(f.ctx, "acme/app", "topic"); held {
		t.Error("a claim on a branch of one repository holds the same branch name in another")
	}
}

func TestLiveClaimsCarryTheirRepositoryAndLeaveOutTheExpired(t *testing.T) {
	f := newClaimFixture(t)
	if _, err := f.s.TakeClaim(f.ctx, f.item.ID, f.human.ID, "laptop", "fix-login", 0); err != nil {
		t.Fatal(err)
	}
	live, err := f.s.LiveClaims(f.ctx)
	if err != nil || len(live) != 1 || live[0].Repo != "acme/app" || live[0].Title != "Fix" {
		t.Fatalf("LiveClaims = %+v, %v", live, err)
	}
	f.now = f.now.Add(DefaultClaimTTL + time.Second)
	if live, err := f.s.LiveClaims(f.ctx); err != nil || len(live) != 0 {
		t.Fatalf("an expired claim is still listed: %+v, %v", live, err)
	}
}
