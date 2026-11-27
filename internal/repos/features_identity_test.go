package repos

import (
	"strings"
	"testing"
)

func identityGen(t *testing.T, src string) (*Output, error) {
	t.Helper()
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return Generate(doc)
}

func TestIdentityValues(t *testing.T) {
	for _, c := range []struct{ name, block, want string }{
		{"off", "identity: off", ""},
		{"marker", "identity: marker", ""},
		{"signed", "identity: signed", ""},
		{"silent is not a value", "identity: silent", "must be one of"},
		{"unknown", "identity: loud", "must be one of"},
		{"not a string", "identity: true", "must be one of"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := identityGen(t, "version: 1\nrepos:\n  acme/app:\n    review: true\n    "+c.block+"\n")
			if c.want == "" && err != nil {
				t.Fatal(err)
			}
			if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
}

func TestIdentityIsOffWhenAbsent(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Resolve(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := EffectiveIdentity(rs[0]); got != "off" {
		t.Fatalf("got %s, want off", got)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Rules[0].Inputs["identity"]; ok {
		t.Fatalf("an absent key must not reach the rules: %+v", out.Rules[0].Inputs)
	}
}

func TestIdentityFromDefaultsAndProfiles(t *testing.T) {
	src := "version: 1\ndefaults:\n  identity: marker\nprofiles:\n  loud:\n    identity: signed\n    review: true\nrepos:\n  acme/a:\n    review: true\n  acme/b:\n    use: loud\n  acme/c:\n    review: true\n    identity: off\n"
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Resolve(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"marker", "signed", "off"}
	for i, w := range want {
		if got := EffectiveIdentity(rs[i]); got != w {
			t.Fatalf("%s: got %s, want %s", rs[i].Name, got, w)
		}
	}
}

func TestIdentityReachesTheRules(t *testing.T) {
	out, err := identityGen(t, "version: 1\nrepos:\n  acme/app:\n    review: true\n    identity: signed\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rules) == 0 || out.Rules[0].Inputs["identity"] != "signed" {
		t.Fatalf("rules = %+v", out.Rules)
	}
}

func TestIdentityMinimumUnderTrust(t *testing.T) {
	for _, level := range []string{"client", "strict"} {
		for _, c := range []struct{ name, extra, want string }{
			{"absent", "", "needs marker or signed"},
			{"off", "\n    identity: off", "needs marker or signed"},
			{"marker", "\n    identity: marker", ""},
			{"signed", "\n    identity: signed", ""},
		} {
			block := "trust: " + level + "\n    executor: server\n    sandbox: {}" + c.extra
			doc, err := Parse([]byte(trustDoc(level, block)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Generate(doc)
			if c.want == "" && err != nil {
				t.Fatalf("%s/%s: %v", level, c.name, err)
			}
			if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), level)) {
				t.Fatalf("%s/%s: error = %v", level, c.name, err)
			}
		}
	}
}

func TestIdentityOffIsAllowedOnOpenRepositories(t *testing.T) {
	if _, err := identityGen(t, "version: 1\nrepos:\n  acme/app:\n    trust: open\n    review: true\n    identity: off\n"); err != nil {
		t.Fatal(err)
	}
}
