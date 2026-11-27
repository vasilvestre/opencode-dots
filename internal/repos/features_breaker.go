package repos

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// FileBreaker is the file bin/breaker check-all reads: one line per repository.
const FileBreaker = "breaker.list"

func init() { Register(breaker{}) }

// breaker is the optional circuit breaker of ADR 0040: when on, a revert of a
// merge made by the dot, or a red default branch after one, drops the
// repository to `act` (bin/breaker). It only exists at `auto`.
type breaker struct{ base }

func (breaker) Key() string { return "circuit_breaker" }
func (breaker) Doc() string {
	return "drop to act after a revert or a red default branch following a merge by the dot (bin/breaker); true, or {subjects: [label or path, ...]} that trip it alone; needs autonomy auto (docs/breaker.md)"
}

func (breaker) Check(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("must be true or a block with `subjects`")
	}
	for k, val := range m {
		if k != "subjects" {
			return fmt.Errorf("unknown key %q: only `subjects` is accepted", k)
		}
		list, ok := val.([]any)
		if !ok || len(list) == 0 {
			return errors.New("subjects must be a non-empty list of labels or paths")
		}
		for _, e := range list {
			s, ok := e.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return errors.New("subjects must be a list of non-empty strings")
			}
			if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == ',' }) {
				return fmt.Errorf("subject %q contains a comma or control character", s)
			}
		}
	}
	return nil
}

// Emit adds the repository to breaker.list: `owner/repo`, or
// `owner/repo=subject,subject` when subjects are given.
func (breaker) Emit(r *Resolved, out *Output) error {
	line := r.Name
	if m, _ := r.Values["circuit_breaker"].(map[string]any); m != nil {
		if list, _ := m["subjects"].([]any); len(list) > 0 {
			parts := make([]string, len(list))
			for i, e := range list {
				parts[i] = strings.TrimSpace(e.(string))
			}
			line += "=" + strings.Join(parts, ",")
		}
	}
	out.Files[FileBreaker] = append(out.Files[FileBreaker], line)
	return nil
}

// Modify refuses the breaker below `auto`, where the dot never merges, and
// hands the rules a `breaker` input: "on", or "on:" and the subjects joined by
// commas. A workflow that does not declare the input ignores it (ADR 0033).
func (breaker) Modify(r *Resolved, out *Output, from int) error {
	if breaker.Check(breaker{}, r.Values["circuit_breaker"]) != nil {
		return nil //nolint:nilerr // Check already reported it
	}
	if lvl := EffectiveAutonomy(r); lvl != "auto" {
		return fmt.Errorf("needs autonomy auto, this repository is at %s and never merges; remove circuit_breaker or raise autonomy", lvl)
	}
	val := "on"
	if m, _ := r.Values["circuit_breaker"].(map[string]any); m != nil {
		if list, _ := m["subjects"].([]any); len(list) > 0 {
			parts := make([]string, len(list))
			for i, e := range list {
				parts[i] = strings.TrimSpace(e.(string))
			}
			val = "on:" + strings.Join(parts, ",")
		}
	}
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["breaker"] = val
	}
	return nil
}
