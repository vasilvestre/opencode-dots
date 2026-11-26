package repos

import (
	"strings"
	"testing"
)

func upstreamGen(t *testing.T, block string) (*Output, error) {
	t.Helper()
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n    " + block + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return Generate(doc)
}

func TestUpstreamValidation(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"autonomy: auto\n    upstream: [{repo: upstream/tool}]", ""},
		{"tend: {auto_merge: true}\n    upstream: [{repo: upstream/tool, max_per_week: 20}]", ""},
		{"autonomy: act\n    upstream: [{repo: upstream/tool}]", "upstream needs autonomy auto, this repository is at act"},
		{"upstream: [{repo: upstream/tool}]", "at act"},
		{"autonomy: propose\n    upstream: [{repo: upstream/tool}]", "at propose"},
		{"autonomy: auto\n    trust: client\n    upstream: [{repo: upstream/tool}]", "refused on a client"},
		{"autonomy: auto\n    trust: strict\n    upstream: [{repo: upstream/tool}]", "refused on a strict"},
		{"autonomy: auto\n    upstream: [{repo: upstream/tool, max_per_week: 0}]", "between 1 and 20"},
		{"autonomy: auto\n    upstream: [{repo: upstream/tool, max_per_week: 21}]", "between 1 and 20"},
		{"autonomy: auto\n    upstream: [{repo: upstream/tool, max_per_week: two}]", "must be a number"},
		{"autonomy: auto\n    upstream: [{repo: upstream}]", "must be owner/name"},
		{"autonomy: auto\n    upstream: [{repo: 'a/b/c'}]", "must be owner/name"},
		{"autonomy: auto\n    upstream: [{repo: upstream/tool}, {repo: Upstream/Tool}]", "listed twice"},
		{"autonomy: auto\n    upstream: [{repo: acme/app}]", "the repository itself"},
		{"autonomy: auto\n    upstream: []", "non-empty list"},
		{"autonomy: auto\n    upstream: [{repo: upstream/tool, days: 3}]", "unknown key"},
		{"autonomy: auto\n    upstream: yes", "non-empty list"},
	} {
		_, err := upstreamGen(t, c.block)
		if c.want == "" && err != nil {
			t.Fatalf("%q: %v", c.block, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("%q: error = %v, want %q", c.block, err, c.want)
		}
	}
}

func TestUpstreamInput(t *testing.T) {
	out, err := upstreamGen(t, "autonomy: auto\n    upstream: [{repo: upstream/tool, max_per_week: 5}, {repo: upstream/other}]")
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Rules[0].Inputs["upstream"]; got != "upstream/tool=5,upstream/other=2" {
		t.Fatalf("input = %q", got)
	}
	out, err = upstreamGen(t, "autonomy: auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Rules[0].Inputs["upstream"]; ok {
		t.Fatal("upstream input present while the feature is off")
	}
}
