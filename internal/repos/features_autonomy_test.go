package repos

import (
	"strings"
	"testing"
)

func autonomyDoc(settings, block string) string {
	return "version: 1\n" + settings + "repos:\n  acme/app:\n    " + block + "\n"
}

func TestAutonomyValues(t *testing.T) {
	for _, c := range []struct{ name, block, want string }{
		{"act", "autonomy: act\n    review: true", ""},
		{"auto", "autonomy: auto\n    review: true", ""},
		{"observe", "autonomy: observe", ""},
		{"propose", "autonomy: propose", ""},
		{"unknown", "autonomy: wild", "must be one of"},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc, err := Parse([]byte(autonomyDoc("", c.block)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Generate(doc)
			if c.want == "" && err != nil {
				t.Fatal(err)
			}
			if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
}

func TestAutonomyIsDerivedWhenAbsent(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"tend: true", "act"},
		{"tend: {auto_merge: true}", "auto"},
		{"tend: {auto_merge: true}\n    autonomy: act", "act"},
	} {
		doc, err := Parse([]byte(autonomyDoc("", c.block)))
		if err != nil {
			t.Fatal(err)
		}
		rs, err := Resolve(doc)
		if err != nil {
			t.Fatal(err)
		}
		if got := EffectiveAutonomy(rs[0]); got != c.want {
			t.Fatalf("%q: got %s, want %s", c.block, got, c.want)
		}
	}
}

func TestAutonomyFromDefaultsAndProfiles(t *testing.T) {
	src := "version: 1\ndefaults:\n  autonomy: act\nprofiles:\n  mine:\n    autonomy: auto\n    review: true\nrepos:\n  acme/a:\n    review: true\n  acme/b:\n    use: mine\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Resolve(doc)
	if err != nil {
		t.Fatal(err)
	}
	if EffectiveAutonomy(rs[0]) != "act" || EffectiveAutonomy(rs[1]) != "auto" {
		t.Fatalf("got %s, %s", EffectiveAutonomy(rs[0]), EffectiveAutonomy(rs[1]))
	}
}

func TestAutonomyAutoIsRefusedUnderTrust(t *testing.T) {
	for _, level := range []string{"client", "strict"} {
		block := "trust: " + level + "\n    executor: server\n    autonomy: auto"
		doc, err := Parse([]byte(trustDoc(level, block)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Generate(doc); err == nil || !strings.Contains(err.Error(), "autonomy: auto is refused on a "+level+" repository") {
			t.Fatalf("%s: error = %v", level, err)
		}
	}
}

func TestAutonomyObserveIsAllowedUnderTrust(t *testing.T) {
	for _, level := range []string{"client", "strict"} {
		for _, a := range []string{"observe", "act"} {
			block := "trust: " + level + "\n    executor: server\n    autonomy: " + a
			doc, err := Parse([]byte(trustDoc(level, block)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Generate(doc); err != nil {
				t.Fatalf("%s/%s: %v", level, a, err)
			}
		}
	}
}

func TestAutonomyReachesTheRules(t *testing.T) {
	doc, err := Parse([]byte(autonomyDoc("", "autonomy: auto\n    review: true")))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rules) == 0 || out.Rules[0].Inputs["autonomy"] != "auto" {
		t.Fatalf("rules = %+v", out.Rules)
	}
}

func TestLowerAutonomyNeverRaises(t *testing.T) {
	if LowerAutonomy("act", "auto") != "act" || LowerAutonomy("auto", "act") != "act" {
		t.Fatal("override must only lower")
	}
}
