package repos

import (
	"strings"
	"testing"
)

func TestSentryList(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    sentry: acme/web\n  acme/lib:\n    review: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Files[FileSentry], "\n"); got != "acme/web acme/app" {
		t.Fatalf("sentry.list = %q", got)
	}
}

func TestSentryRejectsAnythingButOrgSlashProject(t *testing.T) {
	for _, v := range []string{"web", "true", "Acme/Web", "a/b/c"} {
		doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n    sentry: " + v + "\n"))
		if err == nil {
			_, err = Generate(doc)
		}
		if err == nil || !strings.Contains(err.Error(), "org/project") {
			t.Fatalf("%q: error = %v", v, err)
		}
	}
}
