package repos

import (
	"strings"
	"testing"
)

func TestJiraList(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    jira: ABC\n  acme/lib:\n    review: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Files[FileJira], "\n"); got != "ABC acme/app" {
		t.Fatalf("jira.list = %q", got)
	}
}

func TestJiraRejectsAnythingButAProjectKey(t *testing.T) {
	for _, v := range []string{"abc", "true", "A", "ABC-1", "'ABC acme/x'"} {
		doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    review: true\n    jira: " + v + "\n"))
		if err == nil {
			_, err = Generate(doc)
		}
		if err == nil || !strings.Contains(err.Error(), "project key") {
			t.Fatalf("%q: error = %v", v, err)
		}
	}
}

// A client project with two Jira projects for one repository: one line each, the same repository.
func TestJiraSeveralProjectsForOneRepository(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    jira: [ABC, DEF]\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Files[FileJira], "\n"); got != "ABC acme/app\nDEF acme/app" {
		t.Fatalf("jira.list = %q", got)
	}
	for _, v := range []string{"[ABC, ABC]", "[ABC, abc]", "[]"} {
		doc, err := Parse([]byte("version: 1\nrepos:\n  acme/app:\n    jira: " + v + "\n"))
		if err == nil {
			_, err = Generate(doc)
		}
		if err == nil {
			t.Fatalf("%s should be refused", v)
		}
	}
}
