package repos

import (
	"errors"
	"fmt"
)

func init() { Register(autonomy{}) }

// autonomyLevels goes from the least to the most the dot may do (ADR 0040).
var autonomyLevels = []string{"observe", "propose", "act", "auto"}

// autonomy is the ceiling of what a dot may do on a repository (ADR 0040).
// observe writes an internal note, propose holds the artifact until a person
// releases it with the publish-held workflow (ADR 0040).
type autonomy struct{ base }

func (autonomy) Key() string { return "autonomy" }
func (autonomy) Doc() string {
	return "ceiling of what the dot may do here: observe (internal notes only, nothing published), propose (prepares, holds each publication until a person releases it with publish-held), act (pull requests and comments, never merges) or auto (also merges); absent: auto when tend.auto_merge is true, else act (ADR 0040)"
}

func (autonomy) Check(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be one of %v", autonomyLevels)
	}
	switch s {
	case "observe", "propose", "act", "auto":
		return nil
	}
	return fmt.Errorf("must be one of %v", autonomyLevels)
}

func (autonomy) Emit(*Resolved, *Output) error { return nil }

// Modify refuses `auto` under a trust level that refuses auto_merge, and hands
// the effective value to the rules as an `autonomy` input. A workflow that does
// not declare the input ignores it (ADR 0033).
func (autonomy) Modify(r *Resolved, out *Output, from int) error {
	level, _ := r.Values["autonomy"].(string)
	if t, _ := r.Values["trust"].(string); level == "auto" && (t == "client" || t == "strict") {
		return errors.New("auto is refused on a " + t + " repository")
	}
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["autonomy"] = level
	}
	return nil
}

// EffectiveAutonomy is the ceiling of a repository: the explicit key, else
// `auto` when tend.auto_merge is on, else `act`, so no existing config changes.
func EffectiveAutonomy(r *Resolved) string {
	if s, ok := r.Values["autonomy"].(string); ok && s != "" {
		return s
	}
	if m, ok := r.Values["tend"].(map[string]any); ok {
		if auto, _ := m["auto_merge"].(bool); auto {
			return "auto"
		}
	}
	return "act"
}

// LowerAutonomy returns the lower of two levels: a per-workflow override may
// lower the ceiling, never raise it. Not wired to a workflow block yet.
func LowerAutonomy(ceiling, asked string) string {
	if autonomyIndex(asked) < autonomyIndex(ceiling) {
		return asked
	}
	return ceiling
}

func autonomyIndex(s string) int {
	for i, l := range autonomyLevels {
		if l == s {
			return i
		}
	}
	return -1
}
