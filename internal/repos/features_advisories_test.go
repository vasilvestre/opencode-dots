package repos

import (
	"strings"
	"testing"
)

func TestAdvisoriesList(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    advisories: true\n  acme/lib:\n    review: true\n  acme/web:\n    advisories: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Files[FileAdvisory], "\n"); got != "acme/app\nacme/web" {
		t.Fatalf("advisory.list lines = %q", got)
	}
	files, err := Render(out, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(files[FileAdvisory])
	if !strings.Contains(body, "acme/app\nacme/web\n") || strings.Contains(body, "acme/lib") {
		t.Fatalf("advisory.list = %q", body)
	}
}

func TestAdvisoriesOffByDefault(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Files[FileAdvisory]; ok {
		t.Fatal("advisory.list generated while no repository asked for it")
	}
}

func TestAdvisoriesAcceptsTrueOnly(t *testing.T) {
	for _, c := range []struct{ val, want string }{
		{"true", ""},
		{"{every: 1d}", "must be true"},
		{"yes please", "must be true"},
		{"[a]", "must be true"},
	} {
		doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n    advisories: " + c.val + "\n"))
		if err == nil {
			_, err = Generate(doc)
		}
		if c.want == "" && err != nil {
			t.Fatalf("%q: %v", c.val, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("%q: error = %v, want %q", c.val, err, c.want)
		}
	}
}
