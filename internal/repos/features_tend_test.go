package repos

import (
	"strings"
	"testing"
)

func tendLines(t *testing.T, blocks string) []string {
	t.Helper()
	doc, err := Parse([]byte("version: 1\nrepos:\n" + blocks))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range out.Tend {
		lines = append(lines, tendLine(e))
	}
	return lines
}

func TestTendListLines(t *testing.T) {
	got := tendLines(t, `  acme/plain:
    tend: true
  acme/merge:
    tend: {auto_merge: true}
  acme/ceiling:
    tend: true
    autonomy: propose
  acme/up:
    tend: {auto_merge: true}
    autonomy: auto
    upstream: [{repo: up/tool}, {repo: o/x, max_per_week: 5}]
`)
	want := []string{
		"acme/plain false",
		"acme/merge true", // derived auto is not a ceiling: nothing new
		"acme/ceiling false autonomy=propose",
		"acme/up true autonomy=auto upstream=up/tool=2,o/x=5",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tend lines\n got %q\nwant %q", got, want)
	}
}

func TestTendListUpstreamMatchesRuleInput(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    tend: true\n    review: true\n    autonomy: auto\n    upstream: [{repo: up/tool, max_per_week: 3}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if out.Tend[0].Upstream == "" || out.Tend[0].Upstream != out.Rules[0].Inputs["upstream"] {
		t.Fatalf("tend %q, rule %q", out.Tend[0].Upstream, out.Rules[0].Inputs["upstream"])
	}
}
