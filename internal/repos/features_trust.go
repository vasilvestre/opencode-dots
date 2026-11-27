package repos

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func init() { Register(trust{}) }

// trustLevels goes from the most open to the most closed, so an index compare
// says whether one machine may hold a repository (ADR 0032).
var trustLevels = []string{"open", "client", "private", "strict"}

func levelIndex(s string) int {
	for i, name := range trustLevels {
		if name == s {
			return i
		}
	}
	return -1
}

// trust says how far the code of a repository may travel (ADR 0032). It emits
// nothing: Modify refuses a block that contradicts the level, before any file is
// written. It checks the configuration, not a running agent: the isolation that
// holds today is the machine boundary of ADR 0029.
type trust struct{}

func (trust) Key() string { return "trust" }
func (trust) Doc() string {
	return "how far this repository's code may travel: open (any model, any executor), client (only settings.trust.allow, no auto_merge), strict (as client, no local checkout, only where settings.trust.host is strict)"
}

func (trust) Check(v any) error {
	s, ok := v.(string)
	if !ok || levelIndex(s) < 0 {
		return fmt.Errorf("must be one of %s", strings.Join(trustLevels, ", "))
	}
	return nil
}

func (trust) Emit(*Resolved, *Output) error { return nil }

// Modify runs after every other feature of this repository, so it sees the rules
// the workflow blocks produced and the emitter choices (`tend`, `checkout`).
func (trust) Modify(r *Resolved, out *Output, from int) error {
	level, _ := r.Values["trust"].(string)
	if level == "" || level == "open" {
		return nil
	}
	host := r.Settings.Trust.Host
	if host == "" {
		host = "open"
	}
	allow := r.Settings.Trust.Allow
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if levelIndex(host) < levelIndex(level) {
		add("settings.trust.host is %s, which may not hold a %s repository", host, level)
	}
	// The provider chooses the CLI that sends the code to a model. Absent, it is
	// opencode, so that is the value a client repository has to allow.
	provider := "opencode"
	if p, ok := r.Values["provider"].(string); ok && p != "" {
		provider = p
	}
	if !inList(allow.Providers, provider) {
		add("provider %q is not in settings.trust.allow.providers", provider)
	}
	// The code has to stay on a machine the owner named: an executor is
	// required, and the watching machine alone is not one.
	if name, _ := r.Values["executor"].(string); name == "" {
		add("a %s repository must name an executor from settings.trust.allow.executors", level)
	} else if !inList(allow.Executors, name) {
		add("executor %q is not in settings.trust.allow.executors", name)
	}
	if m, ok := r.Values["tend"].(map[string]any); ok {
		if auto, _ := m["auto_merge"].(bool); auto {
			add("auto_merge is refused on a %s repository", level)
		}
	}
	// Europe, at least: the machine that watches and the one that holds the code (ADR 0050).
	if !inEurope(r.Settings.Trust.Region) {
		add("settings.trust.region %q is not in Europe: a %s repository needs a machine in the EU (a value starting with eu)", r.Settings.Trust.Region, level)
	}
	if name, _ := r.Values["executor"].(string); name != "" && !inEurope(r.Settings.Trust.Regions[name]) {
		add("executor %q has region %q in settings.trust.regions: a %s repository needs an executor in Europe", name, r.Settings.Trust.Regions[name], level)
	}
	// Zero data retention: whatever model reads the code must come from a provider the owner has declared
	// as such, and a private or strict repository only from one the owner hosts.
	if provider == "claude" {
		checkModelProvider(level, "provider claude", "anthropic", r.Settings.Trust, add)
	}
	for i := from; i < len(out.Rules); i++ {
		for _, key := range sortedKeys(out.Rules[i].Inputs) {
			if strings.HasSuffix(key, "model") {
				checkModelProvider(level, fmt.Sprintf("%s %q", key, out.Rules[i].Inputs[key]), modelProvider(out.Rules[i].Inputs[key], provider), r.Settings.Trust, add)
			}
		}
	}
	// A model set on a rule is the only model this file can see: the resolved
	// defaults live in the workflow files, which this package does not read.
	for i := from; i < len(out.Rules); i++ {
		for _, key := range sortedKeys(out.Rules[i].Inputs) {
			if strings.HasSuffix(key, "model") && !inList(allow.Models, out.Rules[i].Inputs[key]) {
				add("%s %q is not in settings.trust.allow.models", key, out.Rules[i].Inputs[key])
			}
		}
	}
	// Another party's code is pushed to only after the patch guard has looked at the change.
	if _, ok := r.Values["sandbox"]; !ok {
		add("a %s repository needs a `sandbox` block (an empty one takes the defaults): its pushes go through the patch guard", level)
	}
	if level == "strict" {
		if _, ok := r.Values["checkout"]; ok {
			add("a strict repository keeps no local checkout on a workstation")
		}
	}
	if err := identityFloor(r, level); err != nil {
		add("%v", err)
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return errors.New(strings.Join(problems, "\n"))
}

// inEurope accepts a region that starts with eu (eu, eu-west, eu-fr-par...).
func inEurope(region string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(region)), "eu")
}

// modelProvider is who serves a model: the prefix of `provider/model`, or Anthropic for a bare name given
// to the claude CLI.
func modelProvider(model, provider string) string {
	if i := strings.Index(model, "/"); i > 0 {
		return model[:i]
	}
	if provider == "claude" {
		return "anthropic"
	}
	return ""
}

// checkModelProvider reports a model provider that breaks the zero retention rule of the level.
func checkModelProvider(level, what, prov string, t TrustSettings, add func(string, ...any)) {
	self := inList(t.SelfHosted, prov)
	if level == "private" || level == "strict" {
		if !self {
			add("%s: provider %q is not in settings.trust.self_hosted, and a %s repository only uses a model the owner hosts", what, prov, level)
		}
		return
	}
	if !self && !inList(t.ZDR, prov) {
		add("%s: provider %q has no zero data retention declared in settings.trust.zdr, which a %s repository requires", what, prov, level)
	}
}

// checkTrust validates settings.trust before any file is generated: a host
// level nobody knows, or an empty entry in the allow-list, is a silent hole.
func checkTrust(s Settings) error {
	var problems []string
	if h := s.Trust.Host; h != "" && levelIndex(h) < 0 {
		problems = append(problems, fmt.Sprintf("settings.trust.host: %q must be one of %s", h, strings.Join(trustLevels, ", ")))
	}
	for name, region := range s.Trust.Regions {
		if _, ok := s.Executors[name]; !ok {
			problems = append(problems, fmt.Sprintf("settings.trust.regions: %q is not an executor in settings.executors", name))
		}
		if strings.TrimSpace(region) == "" {
			problems = append(problems, fmt.Sprintf("settings.trust.regions.%s: the region is empty", name))
		}
	}
	for _, l := range []struct {
		name string
		vals []string
	}{{"zdr", s.Trust.ZDR}, {"self_hosted", s.Trust.SelfHosted}} {
		for _, v := range l.vals {
			if strings.TrimSpace(v) != v || v == "" || strings.Contains(v, "/") {
				problems = append(problems, fmt.Sprintf("settings.trust.%s: %q must be a provider name (no slash, no padding)", l.name, v))
			}
		}
	}
	lists := []struct {
		name string
		vals []string
	}{
		{"providers", s.Trust.Allow.Providers},
		{"models", s.Trust.Allow.Models},
		{"executors", s.Trust.Allow.Executors},
	}
	for _, l := range lists {
		for _, v := range l.vals {
			if strings.TrimSpace(v) != v || v == "" {
				problems = append(problems, fmt.Sprintf("settings.trust.allow.%s: %q is empty or padded", l.name, v))
			}
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func inList(list []string, v string) bool {
	return slices.Contains(list, v)
}
