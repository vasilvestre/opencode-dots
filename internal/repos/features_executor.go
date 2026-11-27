package repos

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// executorName is a plain, lower-case word: it becomes part of a file name.
var executorName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func init() { Register(executor{}) }

// executor says which awf serve runs the tasks of the watch rules of this
// repository (ADR 0031). It emits nothing: it tags the rules, and Render writes
// one awf-gh file per executor, since awf-gh takes a single awf_url per file.
type executor struct{}

func (executor) Key() string { return "executor" }
func (executor) Doc() string {
	return "name of the awf serve that runs this repository's tasks, from settings.executors; the watcher stays where it is (awf-gh.<name>.yaml). tend always runs on the watching machine"
}

func (executor) Check(v any) error {
	name, ok := v.(string)
	if !ok || !executorName.MatchString(name) {
		return errors.New("must be a lower-case word of letters, digits and dashes, naming an entry of settings.executors")
	}
	return nil
}

func (executor) Emit(r *Resolved, _ *Output) error {
	name := r.Values["executor"].(string)
	if _, known := r.Settings.Executors[name]; !known {
		return fmt.Errorf("unknown executor %q (settings.executors has %s)", name, knownExecutors(r.Settings))
	}
	return nil
}

func (executor) Modify(r *Resolved, out *Output, from int) error {
	name := r.Values["executor"].(string)
	for i := from; i < len(out.Rules); i++ {
		out.Rules[i].Executor = name
	}
	return nil
}

func knownExecutors(s Settings) string {
	if len(s.Executors) == 0 {
		return "none"
	}
	names := slices.Sorted(maps.Keys(s.Executors))
	return strings.Join(names, ", ")
}

// checkExecutors validates settings.executors before any file is generated: a
// bad name would make a bad file name, a bad address would stop every pass.
func checkExecutors(s Settings) error {
	var problems []string
	for name, addr := range s.Executors {
		if !executorName.MatchString(name) {
			problems = append(problems, fmt.Sprintf("settings.executors: %q must be a lower-case word of letters, digits and dashes", name))
			continue
		}
		u, err := url.Parse(addr)
		bad := err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			strings.ContainsFunc(addr, unicode.IsControl)
		if bad {
			problems = append(problems, fmt.Sprintf("settings.executors.%s: %q is not an http(s) address", name, addr))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}
