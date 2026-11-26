package repos

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

func init() { Register(upstream{}) }

// upstreamRepo is an owner/name as GitHub accepts it.
var upstreamRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// upstream lets the dot report defects it meets in the repositories this one
// depends on, by creating issues there (ADR 0043). It is off unless the key is
// present, and it only exists at autonomy `auto`: a tracker nobody here
// controls is the widest write the dot has.
type upstream struct{ base }

func (upstream) Key() string { return "upstream" }
func (upstream) Doc() string {
	return "upstream repositories the dot may file issues on (create only), a list of {repo: owner/name, max_per_week: 1..20, default 2; one a day at most}; needs autonomy auto and trust open (ADR 0043, docs/upstream.md)"
}

// upstreamEntries reads the list as (repo, cap) pairs.
func upstreamEntries(v any) ([][2]string, error) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("must be a non-empty list of {repo, max_per_week}")
	}
	var out [][2]string
	seen := map[string]bool{}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, errors.New("each entry must be a block with `repo` and optionally `max_per_week`")
		}
		for k := range m {
			if k != "repo" && k != "max_per_week" {
				return nil, fmt.Errorf("unknown key %q: only `repo` and `max_per_week` are accepted", k)
			}
		}
		repo, _ := m["repo"].(string)
		if !upstreamRepo.MatchString(repo) {
			return nil, fmt.Errorf("repo %q must be owner/name", repo)
		}
		if seen[strings.ToLower(repo)] {
			return nil, fmt.Errorf("repo %q is listed twice", repo)
		}
		seen[strings.ToLower(repo)] = true
		n := 2
		if raw, set := m["max_per_week"]; set {
			switch x := raw.(type) {
			case int:
				n = x
			case int64:
				n = int(x)
			case uint64:
				n = int(x)
			case float64:
				if x != float64(int(x)) {
					return nil, fmt.Errorf("max_per_week for %s must be a whole number", repo)
				}
				n = int(x)
			default:
				return nil, fmt.Errorf("max_per_week for %s must be a number", repo)
			}
			if n < 1 || n > 20 {
				return nil, fmt.Errorf("max_per_week for %s must be between 1 and 20, got %d", repo, n)
			}
		}
		out = append(out, [2]string{repo, fmt.Sprint(n)})
	}
	return out, nil
}

func (upstream) Check(v any) error {
	_, err := upstreamEntries(v)
	return err
}

func (upstream) Emit(*Resolved, *Output) error { return nil }

// Modify refuses the key below `auto`, under trust client or strict, and for
// the repository itself (its own tracker is not upstream), then hands the rules
// an `upstream` input: `owner/name=cap` joined by commas. A workflow that does
// not declare the input ignores it (ADR 0033).
func (upstream) Modify(r *Resolved, out *Output, from int) error {
	entries, err := upstreamEntries(r.Values["upstream"])
	if err != nil {
		return nil // Check already reported it
	}
	if lvl := EffectiveAutonomy(r); lvl != "auto" {
		return fmt.Errorf("upstream needs autonomy auto, this repository is at %s; remove upstream or raise autonomy", lvl)
	}
	if t, _ := r.Values["trust"].(string); t == "client" || t == "strict" {
		return fmt.Errorf("upstream is refused on a %s repository: its code and findings stay inside", t)
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		if strings.EqualFold(e[0], r.Name) {
			return fmt.Errorf("upstream lists the repository itself (%s): its own tracker is not upstream", e[0])
		}
		parts[i] = e[0] + "=" + e[1]
	}
	val := strings.Join(parts, ",")
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["upstream"] = val
	}
	return nil
}
