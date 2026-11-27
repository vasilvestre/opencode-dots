package repos

import "fmt"

func init() { Register(identity{}) }

// identityModes goes from the least to the most the dot says about itself in
// GitHub (ADR 0034). There is no `silent`: once signing is on, a marker is always
// kept, because the merge scope reads it.
var identityModes = []string{"off", "marker", "signed"}

// identity says whether the dot marks what it writes to GitHub, so a pull request
// of the dot can be told from a human's under the same login (docs/identity.md).
type identity struct{}

func (identity) Key() string { return "identity" }
func (identity) Doc() string {
	return "how the dot marks what it writes to GitHub: off (nothing, the default), marker (an invisible marker in each body) or signed (a short visible footer too); client and strict repositories need marker at least (ADR 0034)"
}

func (identity) Check(v any) error {
	s, ok := v.(string)
	if !ok || identityIndex(s) < 0 {
		return fmt.Errorf("must be one of %v", identityModes)
	}
	return nil
}

func (identity) Emit(*Resolved, *Output) error { return nil }

// Modify hands an explicit value to the rules as an `identity` input; a workflow
// that does not declare the input ignores it. An absent key emits nothing, so a
// repository that never heard of it keeps the file it had.
func (identity) Modify(r *Resolved, out *Output, from int) error {
	mode, _ := r.Values["identity"].(string)
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["identity"] = mode
	}
	return nil
}

// EffectiveIdentity is the mode of a repository: the explicit key, else off.
func EffectiveIdentity(r *Resolved) string {
	if s, ok := r.Values["identity"].(string); ok && s != "" {
		return s
	}
	return "off"
}

// identityFloor is the minimum under trust `client` and `strict`: the dot's work
// on another party's repository is always marked. The trust feature calls it, since
// an absent `identity` key never reaches this feature's own Modify.
func identityFloor(r *Resolved, level string) error {
	if level != "client" && level != "strict" {
		return nil
	}
	if EffectiveIdentity(r) != "off" {
		return nil
	}
	return fmt.Errorf("identity: a %s repository needs marker or signed, the dot's work there is always marked (it is off or absent)", level)
}

func identityIndex(s string) int {
	for i, m := range identityModes {
		if m == s {
			return i
		}
	}
	return -1
}
