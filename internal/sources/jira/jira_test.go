package jira

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"opencode-dots/internal/sources"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func adapter() Adapter {
	return Adapter{Projects: Projects{"ABC": "acme/shop"}, Now: func() time.Time { return time.Unix(0, 0) }}
}

func parse(t *testing.T, name string) []sources.Envelope {
	t.Helper()
	envs, err := adapter().Parse(fixture(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	for _, e := range envs {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	return envs
}

func payload(t *testing.T, e sources.Envelope) Payload {
	t.Helper()
	var p Payload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreated(t *testing.T) {
	envs := parse(t, "created.json")
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes", len(envs))
	}
	e, p := envs[0], payload(t, envs[0])
	if e.Ref != "jira:ABC-12" || e.Kind != KindCreated || e.Actor != "acc-alice" {
		t.Errorf("envelope = %+v", e)
	}
	if !e.Time.Equal(time.UnixMilli(1709633730000)) {
		t.Errorf("time = %v", e.Time)
	}
	if p.Project != "ABC" || p.Repository != "acme/shop" || p.DiagnosisOnly {
		t.Errorf("mapping = %+v", p)
	}
	if p.Priority != "High" || !reflect.DeepEqual(p.Labels, []string{"web", "regression"}) {
		t.Errorf("fields = %+v", p)
	}
}

func TestUpdatedDiffersFromCreated(t *testing.T) {
	c, u := parse(t, "created.json")[0], parse(t, "updated.json")[0]
	if u.Kind != KindUpdated || c.Kind == u.Kind || c.Key == u.Key {
		t.Errorf("update not distinguishable: %s %s", c.Kind, u.Kind)
	}
	p := payload(t, u)
	if !reflect.DeepEqual(p.Changed, []string{"summary", "status"}) {
		t.Errorf("changed = %v", p.Changed)
	}
	want := "Steps: open /login\nthen press enter.\n@Alice Martin can reproduce"
	if p.Description != want {
		t.Errorf("adf = %q, want %q", p.Description, want)
	}
}

func TestComment(t *testing.T) {
	e := parse(t, "comment.json")[0]
	p := payload(t, e)
	if e.Kind != KindComment || e.Actor != "acc-carol" || p.Comment == nil || p.Comment.Body != "Also happens on iPad." {
		t.Errorf("comment = %+v %+v", e, p)
	}
}

func TestStableKeys(t *testing.T) {
	for _, f := range []string{"created.json", "updated.json", "comment.json", "search.json"} {
		a, b := parse(t, f), parse(t, f)
		for i := range a {
			if a[i].Key != b[i].Key {
				t.Errorf("%s: key not stable", f)
			}
		}
	}
}

func TestUnmappedIsDiagnosisOnly(t *testing.T) {
	p := payload(t, parse(t, "unmapped.json")[0])
	if !p.DiagnosisOnly || p.Repository != "" || p.Project != "ZZZ" {
		t.Errorf("unmapped = %+v", p)
	}
	// The zero adapter maps nothing, and a malformed mapping entry maps nothing.
	for _, a := range []Adapter{{}, {Projects: Projects{"ABC": "not a repo"}}} {
		envs, err := a.Parse(fixture(t, "created.json"))
		if err != nil || !payload(t, envs[0]).DiagnosisOnly {
			t.Errorf("adapter %+v: %v", a, err)
		}
	}
}

func TestHostileDescriptionIsData(t *testing.T) {
	e := parse(t, "hostile.json")[0]
	p := payload(t, e)
	if e.Ref != "jira:ABC-13" || p.Repository != "acme/shop" {
		t.Errorf("text steered the ref or repository: %s %+v", e.Ref, p)
	}
	if !strings.Contains(p.Description, "jira:XYZ-99") || !strings.Contains(p.Description, "evil.example") {
		t.Errorf("description not carried as plain text: %q", p.Description)
	}
	if !reflect.DeepEqual(p.Labels, []string{"repository:evil/repo"}) {
		t.Errorf("labels = %v", p.Labels)
	}
}

func TestSearch(t *testing.T) {
	envs := parse(t, "search.json")
	var kinds []string
	for _, e := range envs {
		kinds = append(kinds, e.Kind+" "+e.Ref)
	}
	want := []string{KindSeen + " jira:ABC-12", KindComment + " jira:ABC-12", KindSeen + " jira:ZZZ-3"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v", kinds)
	}
	if envs[0].Actor != "acc-alice" || envs[2].Actor != Actor {
		t.Errorf("actors = %s %s", envs[0].Actor, envs[2].Actor)
	}
	if !payload(t, envs[2]).DiagnosisOnly || payload(t, envs[1]).Comment.Body != "Also happens on iPad." {
		t.Error("search payloads wrong")
	}
	// A later edit changes the key of the issue, not of its comment.
	later := strings.Replace(string(fixture(t, "search.json")), "11:15:30.456", "13:00:00.000", 1)
	again, err := adapter().Parse([]byte(later))
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Key == envs[0].Key || again[1].Key != envs[1].Key {
		t.Error("update not distinguishable on poll")
	}
}

func TestEmptySearch(t *testing.T) {
	if envs := parse(t, "empty.json"); len(envs) != 0 {
		t.Errorf("got %d envelopes", len(envs))
	}
}

func TestMalformed(t *testing.T) {
	if _, err := adapter().Parse(fixture(t, "malformed.json")); err == nil {
		t.Error("truncated json accepted")
	}
	for _, s := range []string{``, `[]`, `{}`, `{"webhookEvent":"jira:issue_created"}`,
		`{"webhookEvent":"jira:issue_deleted","issue":{"key":"ABC-1"}}`,
		`{"webhookEvent":"jira:issue_created","issue":{"key":"abc-1"}}`,
		`{"webhookEvent":"comment_created","issue":{"key":"ABC-1"}}`} {
		if envs, err := adapter().Parse([]byte(s)); err == nil || len(envs) != 0 {
			t.Errorf("%q: %v %v", s, envs, err)
		}
	}
}

func TestBadKeyLeftOut(t *testing.T) {
	envs, err := adapter().Parse(fixture(t, "badkey.json"))
	if err == nil || len(envs) != 1 || envs[0].Ref != "jira:ABC-14" {
		t.Errorf("got %v, %v", envs, err)
	}
}

func TestFlattenText(t *testing.T) {
	cases := map[string]string{
		``:                     "",
		`null`:                 "",
		`"  plain  "`:          "plain",
		`42`:                   "",
		`{"type":"paragraph"}`: "",
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}`: "a\nb",
	}
	for in, want := range cases {
		if got := FlattenText(json.RawMessage(in)); got != want {
			t.Errorf("FlattenText(%s) = %q, want %q", in, got, want)
		}
	}
	deep := strings.Repeat(`{"type":"blockquote","content":[`, 100) + `{"type":"text","text":"x"}` + strings.Repeat(`]}`, 100)
	if got := FlattenText(json.RawMessage(`{"type":"doc","content":[` + deep + `]}`)); got != "" {
		t.Errorf("deep nesting = %q", got)
	}
}

func TestTruncation(t *testing.T) {
	long := strings.Repeat("é", MaxText)
	raw := `{"webhookEvent":"jira:issue_created","issue":{"key":"ABC-1","fields":{"summary":"s","description":"` + long + `"}}}`
	envs, err := adapter().Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := payload(t, envs[0])
	if !p.Truncated || len(p.Description) > MaxText || !json.Valid(envs[0].Payload) {
		t.Errorf("truncated=%v len=%d", p.Truncated, len(p.Description))
	}
}

func TestProjects(t *testing.T) {
	if err := (Projects{"ABC": "acme/shop"}).Validate(); err != nil {
		t.Error(err)
	}
	for _, p := range []Projects{{"abc": "acme/shop"}, {"ABC": "shop"}, {"ABC": "a b/c"}} {
		if p.Validate() == nil {
			t.Errorf("%v accepted", p)
		}
	}
	if ProjectOf("ABC-12") != "ABC" || ProjectOf("X") != "" {
		t.Error("ProjectOf")
	}
}
