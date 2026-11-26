package repos

import (
	"strings"
	"testing"
)

func breakerGen(t *testing.T, block string) (*Output, error) {
	t.Helper()
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n    " + block + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return Generate(doc)
}

func TestBreakerNeedsAuto(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"circuit_breaker: true", "needs autonomy auto"},
		{"autonomy: act\n    circuit_breaker: true", "needs autonomy auto"},
		{"autonomy: auto\n    circuit_breaker: true", ""},
		{"tend: {auto_merge: true}\n    circuit_breaker: true", ""},
		{"autonomy: auto\n    circuit_breaker: {subjects: [migrations/]}", ""},
		{"autonomy: auto\n    circuit_breaker: {subjects: []}", "non-empty list"},
		{"autonomy: auto\n    circuit_breaker: {subjects: [a, 3]}", "non-empty strings"},
		{"autonomy: auto\n    circuit_breaker: {days: 3}", "unknown key"},
		{"autonomy: auto\n    circuit_breaker: yes please", "true or a block"},
	} {
		_, err := breakerGen(t, c.block)
		if c.want == "" && err != nil {
			t.Fatalf("%q: %v", c.block, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("%q: error = %v, want %q", c.block, err, c.want)
		}
	}
}

func TestBreakerInput(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"autonomy: auto\n    circuit_breaker: true", "on"},
		{"autonomy: auto\n    circuit_breaker: {subjects: [security, db/migrate]}", "on:security,db/migrate"},
	} {
		out, err := breakerGen(t, c.block)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Rules) == 0 || out.Rules[0].Inputs["breaker"] != c.want {
			t.Fatalf("%q: rules = %v, want breaker=%s", c.block, out.Rules, c.want)
		}
	}
	out, err := breakerGen(t, "autonomy: auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Rules[0].Inputs["breaker"]; ok {
		t.Fatal("breaker input present while the feature is off")
	}
}
