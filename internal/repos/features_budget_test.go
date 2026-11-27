package repos

import (
	"strings"
	"testing"
)

func budgetGen(t *testing.T, yaml string) (*Output, error) {
	t.Helper()
	doc, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return Generate(doc)
}

func TestBudgetValidation(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{"{daily_usd: 5}", ""},
		{"{daily_usd: 12.5}", ""},
		{"{daily_usd: 0.01}", ""},
		{"{daily_usd: 10000}", ""},
		{"{daily_usd: 0}", "above 0"},
		{"{daily_usd: -3}", "above 0"},
		{"{daily_usd: 10000.01}", "at most 10000"},
		{"{daily_usd: 1.234}", "at most 2 decimals"},
		{"{daily_usd: lots}", "must be a number"},
		{"{}", "daily_usd is required"},
		{"true", "daily_usd is required"},
		{"{daily_usd: 5, monthly: 9}", "unknown key"},
		{"yes please", "must be a block"},
	} {
		_, err := budgetGen(t, "version: 1\nrepos:\n  acme/app:\n    review: true\n    autonomy: act\n    budget: "+c.value+"\n")
		if c.want == "" && err != nil {
			t.Fatalf("%s: %v", c.value, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("%s: error = %v, want %q", c.value, err, c.want)
		}
	}
}

func TestBudgetRefusedWhereNothingAutonomousIsSpent(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"autonomy: observe", "needs autonomy act or auto"},
		{"autonomy: propose", "needs autonomy act or auto"},
		{"autonomy: act", ""},
		{"autonomy: auto", ""},
		{"tend: true", ""},               // derived act
		{"tend: {auto_merge: true}", ""}, // derived auto
		{"autonomy: propose\n    tend: true", "this repository is at propose"},
	} {
		_, err := budgetGen(t, "version: 1\nrepos:\n  acme/app:\n    review: true\n    budget: {daily_usd: 5}\n    "+c.block+"\n")
		if c.want == "" && err != nil {
			t.Fatalf("%q: %v", c.block, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("%q: error = %v, want %q", c.block, err, c.want)
		}
	}
}

func TestBudgetInputAndTendColumn(t *testing.T) {
	out, err := budgetGen(t, `version: 1
repos:
  acme/app:
    review: true
    tend: true
    autonomy: auto
    budget: {daily_usd: 7.50}
  acme/off:
    review: true
    tend: true
    autonomy: auto
`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Rules[0].Inputs["budget"] != "daily_usd=7.5" {
		t.Fatalf("rule input = %q", out.Rules[0].Inputs["budget"])
	}
	if _, ok := out.Rules[1].Inputs["budget"]; ok {
		t.Fatal("budget input present while the feature is off")
	}
	if got := tendLine(out.Tend[0]); got != "acme/app false autonomy=auto budget=7.5" {
		t.Fatalf("tend line = %q", got)
	}
	if got := tendLine(out.Tend[1]); got != "acme/off false autonomy=auto" {
		t.Fatalf("a repository without budget must keep its line, got %q", got)
	}
}

func TestBudgetFromProfileAndDefaults(t *testing.T) {
	out, err := budgetGen(t, `version: 1
defaults:
  budget: {daily_usd: 3}
profiles:
  company:
    autonomy: act
    budget: {daily_usd: 40}
repos:
  acme/team:
    use: company
    tend: true
    review: true
  acme/override:
    use: company
    tend: true
    review: true
    budget: {daily_usd: 12}
  acme/dflt:
    tend: true
    review: true
    autonomy: act
    budget: true
  acme/none:
    tend: true
    review: true
    autonomy: act
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"acme/team":     "acme/team false autonomy=act budget=40",
		"acme/override": "acme/override false autonomy=act budget=12",
		"acme/dflt":     "acme/dflt false autonomy=act budget=3",
		"acme/none":     "acme/none false autonomy=act",
	}
	for _, e := range out.Tend {
		if got := tendLine(e); got != want[e.Repo] {
			t.Fatalf("%s: tend line = %q, want %q", e.Repo, got, want[e.Repo])
		}
	}
}

func TestBudgetFromProfileIsRefusedBelowAct(t *testing.T) {
	_, err := budgetGen(t, `version: 1
profiles:
  company:
    budget: {daily_usd: 40}
repos:
  acme/watch:
    use: company
    review: true
    autonomy: observe
`)
	if err == nil || !strings.Contains(err.Error(), "acme/watch: budget: needs autonomy act or auto") {
		t.Fatalf("error = %v", err)
	}
}
