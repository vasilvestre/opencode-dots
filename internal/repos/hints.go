package repos

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var hintOwnerRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// checkHints validates settings.checkout_hints: an owner and a folder that is the developer's home
// (`~/...`) or an absolute path, without `..`. A hint is shown to people, never run, but a path that
// walks out of a root would still mislead.
func checkHints(s Settings) error {
	var problems []string
	for owner, path := range s.CheckoutHints {
		switch {
		case !hintOwnerRE.MatchString(owner) || owner == "." || owner == "..":
			problems = append(problems, fmt.Sprintf("settings.checkout_hints: %q is not an owner name", owner))
		case (!strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/")) || strings.Contains(path, "..") || strings.ContainsAny(path, "\n\r=#"):
			problems = append(problems, fmt.Sprintf("settings.checkout_hints.%s: %q must be a folder starting with ~/ or /, without .., = or #", owner, path))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("%s", strings.Join(problems, "\n"))
}

// CheckoutHint is the folder the team suggests for the projects of an owner, if it says so. The owner is
// compared without regard to case, as GitHub does.
func (s Settings) CheckoutHint(owner string) (string, bool) {
	for name, dir := range s.CheckoutHints {
		if strings.EqualFold(name, owner) {
			return dir, true
		}
	}
	return "", false
}
