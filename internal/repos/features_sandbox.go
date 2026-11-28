package repos

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

func init() { Register(sandbox{}) }

const (
	defaultSandboxLines = 400
	maxSandboxLines     = 5000
	maxTestCommand      = 500
)

// sandbox makes the dot check every push on a repository before it goes out (ADR 0032,
// docs/runner.md): the patch goes through bin/patch-guard, and a configured test runs in a
// throwaway container with no network. It is opt-in, and required on a `client` or `strict`
// repository. With no key nothing is checked and no generated file changes.
type sandbox struct{}

func (sandbox) Key() string { return "sandbox" }
func (sandbox) Doc() string {
	return "check every push before it goes out: {max_lines: N (default 400, up to 5000), allow_paths: [globs], allow_locks: true, test: {image: dots-... or dockerfile: Dockerfile [target: dev], command: ...}}; the patch goes through bin/patch-guard (no secrets, CI config, links, binaries, or oversized change) and the test runs in a throwaway container with no network; an empty block takes the defaults; required on a client or strict repository (ADR 0032, docs/runner.md)"
}

func (sandbox) Check(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("must be a block (an empty one takes the defaults)")
	}
	for k, val := range m {
		switch k {
		case "max_lines":
			n, ok := asInt(val)
			if !ok || n < 1 || n > maxSandboxLines {
				return fmt.Errorf("max_lines must be a number from 1 to %d", maxSandboxLines)
			}
		case "allow_paths":
			list, ok := val.([]any)
			if !ok || len(list) == 0 {
				return errors.New("allow_paths must be a non-empty list of path globs")
			}
			for _, g := range list {
				s, ok := g.(string)
				if !ok || s == "" || strings.ContainsAny(s, " \t\n") {
					return fmt.Errorf("allow_paths: %v is not a glob without spaces", g)
				}
			}
		case "allow_locks":
			if _, ok := val.(bool); !ok {
				return errors.New("allow_locks must be true or false")
			}
		case "test":
			if err := checkSandboxTest(val); err != nil {
				return fmt.Errorf("test: %w", err)
			}
		default:
			return fmt.Errorf("unknown key %q: max_lines, allow_paths, allow_locks or test", k)
		}
	}
	return nil
}

var dockerStageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func checkSandboxTest(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("must be a block with `image` or `dockerfile`, and `command`")
	}
	for k := range m {
		if k != "image" && k != "dockerfile" && k != "target" && k != "command" {
			return fmt.Errorf("unknown key %q: image, dockerfile, target or command", k)
		}
	}
	image, _ := m["image"].(string)
	dockerfile, _ := m["dockerfile"].(string)
	switch {
	case image != "" && dockerfile != "":
		return errors.New("give `image` or `dockerfile`, not both")
	case image == "" && dockerfile == "":
		return errors.New("give `image` (a name starting with `dots-`) or `dockerfile` (a path in the repository)")
	case image != "":
		if !strings.HasPrefix(image, "dots-") || strings.ContainsAny(image, " \t\n") {
			return errors.New("image must be a name starting with `dots-` (bin/sandbox-run refuses any other)")
		}
	default:
		if strings.HasPrefix(dockerfile, "/") || strings.Contains(dockerfile, "..") || strings.ContainsAny(dockerfile, " \t\n") {
			return errors.New("dockerfile must be a path inside the repository, with no spaces or `..`")
		}
	}
	if target, has := m["target"]; has {
		name, _ := target.(string)
		if dockerfile == "" {
			return errors.New("target needs a dockerfile")
		}
		if !dockerStageName.MatchString(name) {
			return errors.New("target must be the name of a stage: letters, digits, `_`, `.` or `-`")
		}
	}
	command, _ := m["command"].(string)
	if strings.TrimSpace(command) == "" || len(command) > maxTestCommand || strings.ContainsAny(command, "\n\r") {
		return fmt.Errorf("command must be one line of at most %d characters", maxTestCommand)
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func (sandbox) Emit(*Resolved, *Output) error { return nil }

// Modify hands the rules a `sandbox` input: the block as compact JSON with its defaults filled, in
// base64 so that a quote in a command cannot break the shell line it is placed in, which the workflow
// passes to bin/publish-gate. A workflow that does not declare it ignores it (ADR 0033).
func (sandbox) Modify(r *Resolved, out *Output, from int) error {
	val := sandboxInput(r)
	if val == "" {
		return nil
	}
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["sandbox"] = val
	}
	return nil
}

// sandboxInput is the base64 of the JSON the gate reads, or "" when the repository sets no valid block.
func sandboxInput(r *Resolved) string {
	m, ok := r.Values["sandbox"].(map[string]any)
	if !ok {
		return ""
	}
	out := map[string]any{"max_lines": defaultSandboxLines}
	for k, v := range m {
		if k == "max_lines" {
			n, _ := asInt(v)
			out[k] = n
		} else {
			out[k] = v
		}
	}
	raw, err := json.Marshal(out) // map keys come out sorted: the generated file is stable
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(raw)
}
