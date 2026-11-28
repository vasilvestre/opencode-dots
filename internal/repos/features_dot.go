package repos

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// dotID is a dot's name: it appears in repos.yaml on every server that shares a repository.
var dotID = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

func init() { Register(dotFeature{}) }

// dotFeature names the dot that acts on a repository (ADR 0038, first step). It emits
// nothing itself: Generate skips every repository whose dot is not settings.dot_id, so no
// rule, tend line, checkout, sync line or breaker line is written for it. There is no
// channel between servers; the other dot reads the same key on its own side.
type dotFeature struct{ base }

func (dotFeature) Key() string { return "dot" }
func (dotFeature) Doc() string {
	return "id of the dot that acts on this repository; a server whose settings.dot_id differs generates nothing for it (observe-only); needs settings.dot_id (docs/dots.md)"
}

func (dotFeature) Check(v any) error {
	s, ok := v.(string)
	if !ok || !dotID.MatchString(s) {
		return errors.New("must be a dot id: lower-case letters, digits, `-` and `_`, 1 to 40 characters")
	}
	return nil
}

func (dotFeature) Emit(r *Resolved, _ *Output) error {
	if r.Settings.DotID == "" {
		return errors.New("needs settings.dot_id, this server's own dot id, to tell which repositories are its own")
	}
	return nil
}

// checkDot validates settings.dot_id before any file is generated.
func checkDot(s Settings) error {
	if s.DotID != "" && !dotID.MatchString(s.DotID) {
		return fmt.Errorf("settings.dot_id %q must be lower-case letters, digits, `-` and `_`, 1 to 40 characters", s.DotID)
	}
	return nil
}

// foreignDot reports whether the repository names another dot than this server's. Without a
// dot_id nothing is foreign: Emit refuses the key instead.
func foreignDot(r *Resolved) bool {
	v, ok := r.Values["dot"].(string)
	return ok && r.Settings.DotID != "" && v != r.Settings.DotID
}

// skippedLine is the comment each generated file carries when repositories were left out:
// a count only, never a name.
func skippedLine(n int) string {
	noun := "repositories"
	if n == 1 {
		noun = "repository"
	}
	return fmt.Sprintf("# skipped: %d %s owned by another dot\n", n, noun)
}

// withSkipped puts the skipped comment right under the standard header.
func withSkipped(body []byte, n int) []byte {
	if n == 0 {
		return body
	}
	h := len(commentHeader())
	return append(append(append([]byte{}, body[:h]...), skippedLine(n)...), body[h:]...)
}

// Set changes one of the settings an editor may write: how many items a watcher pass takes
// (nil clears it) and this server's dot id (empty clears it). Trust and executors are edited in the
// file, where their cross-checks can be read.
func (s *Settings) Set(key string, value any) error {
	switch key {
	case "max_per_run":
		if value == nil {
			s.MaxPerRun = 0
			return nil
		}
		n, ok := value.(float64)
		if !ok || n != float64(int(n)) || n < 1 || n > 50 {
			return errors.New("max_per_run is a whole number from 1 to 50")
		}
		s.MaxPerRun = int(n)
	case "dot_id":
		id, _ := value.(string)
		id = strings.TrimSpace(id)
		if id != "" && !dotID.MatchString(id) {
			return errors.New("dot_id is lowercase letters, digits, - and _")
		}
		s.DotID = id
	default:
		return fmt.Errorf("the page does not edit settings.%s: use the repos.yaml editor", key)
	}
	return nil
}
