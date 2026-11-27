package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

func sentryFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "sources", "sentry", "testdata", "list.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIngestSentry(t *testing.T) {
	st, ctx := setup(t)
	raw := sentryFixture(t)
	rs, left, err := IngestSentry(ctx, st, "default", "acme/app", "acme", "web", raw)
	if err != nil || len(rs) != 2 || rs[0].Ref != "sentry:acme/web#4711" || rs[0].Outcome != store.OutcomeNew {
		t.Fatalf("results = %+v, %v", rs, err)
	}
	if left == nil {
		t.Fatal("the unusable item was not reported")
	}
	// A busier issue is not a change; a new status is.
	busier := strings.Replace(strings.Replace(string(raw), `"count":"42"`, `"count":"90"`, 1), "2024-06-10T12:00:00Z", "2024-06-11T00:00:00Z", 1)
	rs, _, _ = IngestSentry(ctx, st, "default", "acme/app", "acme", "web", []byte(busier))
	if rs[0].Outcome != store.OutcomeSeen {
		t.Fatalf("count and last seen made a change: %+v", rs[0])
	}
	resolved := strings.Replace(string(raw), `"status":"unresolved","firstSeen":"2024-05-02`, `"status":"resolved","firstSeen":"2024-05-02`, 1)
	rs, _, _ = IngestSentry(ctx, st, "default", "acme/app", "acme", "web", []byte(resolved))
	if rs[0].Outcome != store.OutcomeChanged {
		t.Fatalf("a new status was not a change: %+v", rs[0])
	}
}

func TestIngestSentryOtherProjectLeftOut(t *testing.T) {
	st, ctx := setup(t)
	rs, left, err := IngestSentry(ctx, st, "default", "acme/app", "acme", "api", sentryFixture(t))
	if err != nil || len(rs) != 0 || left == nil {
		t.Fatalf("results = %+v, left = %v, err = %v", rs, left, err)
	}
}
