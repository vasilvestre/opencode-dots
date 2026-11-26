package advisory

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	a := Adapter{}
	envs, err := a.Parse(fixture(t, "audit.json"))
	if err != nil {
		t.Fatal(err) // the PKSA-3333 entry has a CVE, so nothing is left out
	}
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(envs))
	}
	byRef := map[string]Payload{}
	for _, e := range envs {
		if err := e.Validate(); err != nil {
			t.Error(err)
		}
		var p Payload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		byRef[e.Ref] = p
	}
	g := byRef["advisory:GHSA-cfgh-2345-wxvr"]
	if g.Severity != "critical" || len(g.Packages) != 2 {
		t.Errorf("merged advisory = %+v", g)
	}
	if want := []string{"CVE-2023-22222", "CVE-2024-11111"}; !reflect.DeepEqual(g.CVEs, want) {
		t.Errorf("cves = %v", g.CVEs)
	}
	if want := []string{"1.4.2", "2.1.1"}; !reflect.DeepEqual(g.Packages[1].FixedIn, want) {
		t.Errorf("fixed_in = %v", g.Packages[1].FixedIn)
	}
	c := byRef["advisory:CVE-2022-33333"]
	if c.Severity != "unknown" || c.Packages[0].Name != "acme/cache" {
		t.Errorf("cve-only advisory = %+v", c)
	}
	for _, e := range envs {
		if e.Ref == "advisory:GHSA-cfgh-2345-wxvr" && !e.Time.Equal(time.Date(2023, 5, 2, 8, 30, 0, 0, time.UTC)) {
			t.Errorf("time = %v, want the earliest report", e.Time)
		}
	}
}

// The title is hostile text: it is carried as data and never read for a reference.
func TestTitleIsData(t *testing.T) {
	envs, _ := Adapter{}.Parse(fixture(t, "audit.json"))
	for _, e := range envs {
		if e.Ref != "advisory:GHSA-cfgh-2345-wxvr" && e.Ref != "advisory:CVE-2022-33333" {
			t.Errorf("unexpected ref %s", e.Ref)
		}
	}
}

func TestRepeatedPollSameKeys(t *testing.T) {
	a := Adapter{Now: func() time.Time { return time.Unix(1, 0) }}
	x, _ := a.Parse(fixture(t, "audit.json"))
	y, _ := Adapter{Now: func() time.Time { return time.Unix(99, 0) }}.Parse(fixture(t, "audit.json"))
	for i := range x {
		if x[i].Key != y[i].Key {
			t.Errorf("key changed between polls: %s", x[i].Ref)
		}
	}
}

func TestEmpty(t *testing.T) {
	envs, err := Adapter{}.Parse(fixture(t, "empty.json"))
	if err != nil || len(envs) != 0 {
		t.Errorf("got %v, %v", envs, err)
	}
}

func TestNoUsableID(t *testing.T) {
	envs, err := Adapter{}.Parse(fixture(t, "noid.json"))
	if err == nil || len(envs) != 0 {
		t.Errorf("got %v, %v", envs, err)
	}
}

func TestNotJSON(t *testing.T) {
	if _, err := (Adapter{}).Parse([]byte("Found 1 advisory")); err == nil {
		t.Error("plain text accepted")
	}
}
