package sources

import (
	"fmt"
	"regexp"
)

// Tracker is the part of a reference before the first colon.
type Tracker string

// The trackers a reference may name (ADR 0023, ADR 0039).
const (
	GitHub   Tracker = "github"
	Branch   Tracker = "branch"
	Jira     Tracker = "jira"
	Sentry   Tracker = "sentry"
	Advisory Tracker = "advisory"
)

// grammar holds the key shape each tracker accepts. Keys carry no spaces or control
// characters, so a reference is safe to pass in a shell word and to store.
var grammar = map[Tracker]*regexp.Regexp{
	// owner/repo#12: an issue or a pull request.
	GitHub: regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+#[1-9][0-9]*$`),
	// owner/repo:name: a branch; the name may hold slashes.
	Branch: regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+:[A-Za-z0-9._/-]+$`),
	// ABC-12.
	Jira: regexp.MustCompile(`^[A-Z][A-Z0-9]+-[1-9][0-9]*$`),
	// org/project#id: the id is Sentry's numeric issue id or its short id.
	Sentry: regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9_-]*#[A-Za-z0-9-]+$`),
	// GHSA-xxxx-xxxx-xxxx or CVE-YYYY-NNNN, then @owner/repo: one advisory is one work item
	// per repository, so the repository is part of the key (ADR 0039, reference scoping).
	Advisory: regexp.MustCompile(`^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|CVE-[0-9]{4}-[0-9]{4,})@[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`),
}

// AdvisoryID is the advisory part of an advisory key, without the repository.
var AdvisoryID = regexp.MustCompile(`^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|CVE-[0-9]{4}-[0-9]{4,})$`)

// AdvisoryRef builds advisory:<id>@<owner>/<repo>.
func AdvisoryRef(id, repo string) (Ref, error) { return NewRef(Advisory, id+"@"+repo) }

// LegacyAdvisoryRef is the form `advisory:<id>` used before references were scoped to a
// repository. It is never valid input to ParseRef; it exists so stored items can be found
// and rewritten.
func LegacyAdvisoryRef(id string) (string, error) {
	if !AdvisoryID.MatchString(id) {
		return "", fmt.Errorf("sources: %q is not an advisory id", id)
	}
	return "advisory:" + id, nil
}

// Ref is a parsed reference, tracker:key.
type Ref struct {
	Tracker Tracker
	Key     string
}

// String returns the canonical form, the one stored and compared.
func (r Ref) String() string { return string(r.Tracker) + ":" + r.Key }

// ParseRef validates s against the grammar of its tracker. It never looks the tracker up.
func ParseRef(s string) (Ref, error) {
	for t, re := range grammar {
		prefix := string(t) + ":"
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			key := s[len(prefix):]
			if !re.MatchString(key) {
				return Ref{}, fmt.Errorf("sources: %q is not a valid %s reference", s, t)
			}
			return Ref{Tracker: t, Key: key}, nil
		}
	}
	return Ref{}, fmt.Errorf("sources: %q names no known tracker (github, branch, jira, sentry, advisory)", s)
}

// NewRef builds a reference from its parts and validates it.
func NewRef(t Tracker, key string) (Ref, error) { return ParseRef(string(t) + ":" + key) }
