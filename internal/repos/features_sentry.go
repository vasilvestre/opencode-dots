package repos

import (
	"errors"
	"fmt"
	"regexp"
)

// FileSentry is the file bin/sentry-poll reads: one `org/project owner/repo` per line.
const FileSentry = "sentry.list"

var sentrySlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9_-]*$`)

func init() { Register(sentry{}) }

// sentry maps a Sentry project to the repository: `sentry: org/project` makes its unresolved
// issues work items of this repository (bin/sentry-poll, docs/sources-sentry.md). The mapping is
// never read from an issue. Off by default.
type sentry struct{ base }

func (sentry) Key() string { return "sentry" }
func (sentry) Doc() string {
	return "the Sentry project (org/project slugs, lowercase) whose unresolved issues become work items of this repository, polled by bin/sentry-poll; read only (sentry.list, docs/sources-sentry.md)"
}

func (sentry) Check(v any) error {
	s, ok := v.(string)
	if !ok || !sentrySlug.MatchString(s) {
		return errors.New("must be a Sentry project as org/project, lowercase slugs")
	}
	return nil
}

// Emit adds `org/project owner/repo` to sentry.list.
func (sentry) Emit(r *Resolved, out *Output) error {
	key, _ := r.Values["sentry"].(string)
	if key == "" {
		return fmt.Errorf("sentry: no project for %s", r.Name)
	}
	out.Files[FileSentry] = append(out.Files[FileSentry], key+" "+r.Name)
	return nil
}
