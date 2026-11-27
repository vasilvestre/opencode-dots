package repos

import (
	"errors"
	"fmt"
	"regexp"
)

// FileJira is the file bin/jira-poll reads: one `PROJECT owner/repo` per line.
const FileJira = "jira.list"

var jiraProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)

func init() { Register(jira{}) }

// jira maps a Jira project to the repository: `jira: ABC` makes the tickets of ABC work
// items of this repository (bin/jira-poll, docs/sources-tracker.md). One project feeds one
// repository; the mapping is never read from a ticket. Off by default.
type jira struct{ base }

func (jira) Key() string { return "jira" }
func (jira) Doc() string {
	return "the Jira project key (such as ABC) whose tickets become work items of this repository, polled by bin/jira-poll; read only (jira.list, docs/sources-tracker.md)"
}

func (jira) Check(v any) error {
	s, ok := v.(string)
	if !ok || !jiraProjectKey.MatchString(s) {
		return errors.New("must be a Jira project key such as ABC (capital letters and digits)")
	}
	return nil
}

// Emit adds `PROJECT owner/repo` to jira.list.
func (jira) Emit(r *Resolved, out *Output) error {
	key, _ := r.Values["jira"].(string)
	if key == "" {
		return fmt.Errorf("jira: no project key for %s", r.Name)
	}
	out.Files[FileJira] = append(out.Files[FileJira], key+" "+r.Name)
	return nil
}
