package repos

import (
	"reflect"
	"strings"
	"testing"
)

const marshalDoc = `# a comment that will not survive
version: 1
settings:
  max_per_run: 6
  executors: {pc: "http://127.0.0.1:2512"}
  trust: {host: client, region: eu-west, allow: {providers: [opencode]}}
defaults:
  review:
    ignore_authors: ['bot[bot]']
profiles:
  managed:
    tend: {auto_merge: true}
    review: true
repos:
  acme/managed:
    use: [managed]
    fix_issue: {label: dot}
    review: [{on: [pr-opened], labels: [x]}, {on: [checks-failed]}]
  acme/plain:
    reply: true
  acme/empty: {}
`

func TestMarshalRoundTripsTheDocumentInOrder(t *testing.T) {
	doc, err := Parse([]byte(marshalDoc))
	if err != nil {
		t.Fatal(err)
	}
	out, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatalf("the output does not parse: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(doc, again) {
		t.Fatalf("round trip changed the document\nwant %+v\ngot  %+v\n%s", doc, again, out)
	}
	if strings.Contains(string(out), "awf_url") || strings.Contains(string(out), "dot_id") {
		t.Fatalf("an unset setting was written:\n%s", out)
	}
	for _, want := range []string{"acme/managed", "acme/plain", "acme/empty"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("lost %s:\n%s", want, out)
		}
	}
	if strings.Index(string(out), "acme/managed") > strings.Index(string(out), "acme/plain") {
		t.Fatal("the repositories changed order")
	}
}
