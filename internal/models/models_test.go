package models

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const doc = `version: 1
presets:
    light: acme/small
    default: acme/big
    claude_light: haiku
    claude_default: sonnet
overrides:
    review-pr/address_model: other/fixer
`

func load(t *testing.T, s string) *Configuration {
	t.Helper()
	c, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolveTakesTheOverrideThenThePreset(t *testing.T) {
	c := load(t, doc)
	for key, want := range map[string]string{"ask-pr/model": "acme/small", "tend-repo/model": "acme/big",
		"review-pr/address_model": "other/fixer", "review-pr/claude_review_model": "sonnet", "nope/model": ""} {
		if got := c.Resolve(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseRefusesWhatItCannotRead(t *testing.T) {
	for name, s := range map[string]string{
		"empty":             "",
		"two documents":     doc + "---\nversion: 1\n",
		"unknown key":       doc + "extra: 1\n",
		"version":           strings.Replace(doc, "version: 1", "version: 2", 1),
		"missing preset":    strings.Replace(doc, "    claude_light: haiku\n", "", 1),
		"unknown preset":    strings.Replace(doc, "    light:", "    tiny: acme/x\n    light:", 1),
		"bad model":         strings.Replace(doc, "acme/small", "small", 1),
		"claude with slash": strings.Replace(doc, "haiku", "a/haiku", 1),
		"unknown role":      doc + "    ghost/model: a/b\n",
		"same reviewers":    strings.Replace(doc, "other/fixer", "acme/big", 1),
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestUpdatedValidatesAndLeavesTheOriginal(t *testing.T) {
	c := load(t, doc)
	clear := (*string)(nil)
	set := "x/y"
	next, err := c.Updated(map[string]string{"light": "acme/tiny"}, map[string]*string{"review-pr/address_model": clear, "ask-pr/model": &set})
	if err != nil {
		t.Fatal(err)
	}
	if next.Resolve("ask-pr/model") != "x/y" || next.Resolve("fix-ci/model") != "acme/tiny" || next.Resolve("review-pr/address_model") != "acme/tiny" {
		t.Fatalf("update = %+v", next)
	}
	if c.Resolve("fix-ci/model") != "acme/small" || len(c.Overrides) != 1 {
		t.Fatal("the original changed")
	}
	if _, err := c.Updated(nil, map[string]*string{"review-pr/address_model": &set, "review-pr/review_model": &set}); err == nil {
		t.Fatal("equal reviewers were accepted")
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	c := load(t, doc)
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if got := load(t, string(out)); got.Resolve("review-pr/address_model") != "other/fixer" || got.Presets["claude_default"] != "sonnet" {
		t.Fatalf("round trip lost data: %s", out)
	}
}

func TestVariablesListEveryRole(t *testing.T) {
	vars := load(t, doc).Variables()
	if len(vars) != len(Roles) {
		t.Fatalf("%d variables for %d roles", len(vars), len(Roles))
	}
	for _, v := range vars {
		if (v.Key == "review-pr/address_model") != (v.Override != nil) {
			t.Errorf("%s override = %v", v.Key, v.Override)
		}
	}
}

func TestParseCatalogKeepsValidIDsOnce(t *testing.T) {
	got := parseCatalog([]byte("b/x\n\nnot an id\na/y\nb/x\n"))
	if !slices.Equal(got, []string{"a/y", "b/x"}) {
		t.Fatalf("catalog = %v", got)
	}
}

// The workflows ask bin/model for their role and the preset it is assigned to: that is where the
// assignment is written, so it must be the table's.
func TestWorkflowsAskForTheRolesOfTheTable(t *testing.T) {
	asked := map[string]string{}
	files, _ := filepath.Glob(filepath.Join("..", "..", "workflows", "*.yaml"))
	// `bin/model ROLE PRESET`, or review-pr's `role=ROLE; preset=PRESET` for its two providers.
	re := regexp.MustCompile(`bin/model"? ([A-Za-z0-9_/-]+) ([a-z_]+)|role=([A-Za-z0-9_/-]+); preset=([a-z_]+)`)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			asked[m[1]+m[3]] = m[2] + m[4]
		}
	}
	for _, r := range Roles {
		if asked[r.Key] != r.Preset {
			t.Errorf("%s: workflows ask preset %q, the table says %q", r.Key, asked[r.Key], r.Preset)
		}
	}
	if len(asked) != len(Roles) {
		t.Errorf("workflows ask for %d roles, the table has %d", len(asked), len(Roles))
	}
}
