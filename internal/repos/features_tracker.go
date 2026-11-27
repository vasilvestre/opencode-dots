package repos

import (
	"errors"
	"fmt"
	"regexp"
)

// The files bin/jira-poll and bin/sentry-poll read: one `PROJECT owner/repo` per line.
const (
	FileJira   = "jira.list"
	FileSentry = "sentry.list"
)

// tracker maps a project of an external tracker to the repository: `jira: ABC` or `sentry:
// org/project` makes its tickets or unresolved issues work items of this repository (docs/sources-*.md).
// One project feeds one repository; the mapping is never read from a ticket. Off by default.
type tracker struct {
	base
	key   string
	file  string
	valid *regexp.Regexp
	form  string // what a valid value looks like, for the error
	doc   string
}

func init() {
	Register(tracker{key: "jira", file: FileJira, valid: regexp.MustCompile(`^[A-Z][A-Z0-9]+$`),
		form: "a Jira project key such as ABC (capital letters and digits)",
		doc:  "the Jira project key (such as ABC) whose tickets become work items of this repository, polled by bin/jira-poll; read only (jira.list, docs/sources-tracker.md)"})
	Register(tracker{key: "sentry", file: FileSentry, valid: regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9_-]*$`),
		form: "a sentry project as org/project, lowercase slugs",
		doc:  "the Sentry project (org/project slugs, lowercase) whose unresolved issues become work items of this repository, polled by bin/sentry-poll; read only (sentry.list, docs/sources-sentry.md)"})
}

func (t tracker) Key() string { return t.key }
func (t tracker) Doc() string { return t.doc }

func (t tracker) Check(v any) error {
	if s, ok := v.(string); !ok || !t.valid.MatchString(s) {
		return errors.New("must be " + t.form)
	}
	return nil
}

// Emit adds `PROJECT owner/repo` to the tracker's list.
func (t tracker) Emit(r *Resolved, out *Output) error {
	project, _ := r.Values[t.key].(string)
	if project == "" {
		return fmt.Errorf("%s: no project for %s", t.key, r.Name)
	}
	out.Files[t.file] = append(out.Files[t.file], project+" "+r.Name)
	return nil
}
