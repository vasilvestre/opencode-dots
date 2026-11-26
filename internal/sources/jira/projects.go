package jira

import (
	"fmt"
	"regexp"
	"strings"
)

// Projects maps a Jira project key (ABC) to a repository (owner/repo).
//
// A project that is not in the table is diagnosis-only: its envelopes carry no
// repository and the work may analyse but never branch or push. The adapter never
// derives a repository from anything else, least of all from issue text.
type Projects map[string]string

var (
	projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)
	repoName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
)

// Validate reports the first malformed entry.
func (p Projects) Validate() error {
	for k, r := range p {
		if !projectKey.MatchString(k) {
			return fmt.Errorf("jira: %q is not a project key", k)
		}
		if !repoName.MatchString(r) {
			return fmt.Errorf("jira: project %s maps to %q, not owner/repo", k, r)
		}
	}
	return nil
}

// Repository returns the repository of a project, or "" when it is unmapped or its
// entry is malformed.
func (p Projects) Repository(project string) string {
	r := p[project]
	if !repoName.MatchString(r) {
		return ""
	}
	return r
}

// ProjectOf returns the project key of an issue key: the part before the last dash.
func ProjectOf(issueKey string) string {
	i := strings.LastIndex(issueKey, "-")
	if i < 0 {
		return ""
	}
	return issueKey[:i]
}
