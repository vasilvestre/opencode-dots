package repos

import "errors"

// FileAdvisory is the file bin/advisories reads: one `owner/repo` per line.
const FileAdvisory = "advisory.list"

func init() { Register(advisories{}) }

// advisories turns on the audit of vulnerable dependencies (bin/advisories, docs/advisories.md):
// the repository is listed in advisory.list, and nothing else changes. Off by default.
type advisories struct{ base }

func (advisories) Key() string { return "advisories" }
func (advisories) Doc() string {
	return "audit composer.lock for vulnerable dependencies and track each advisory as a work item (bin/advisories); true only, never upgrades anything (advisory.list, docs/advisories.md)"
}

func (advisories) Check(v any) error {
	if m, ok := v.(map[string]any); !ok || len(m) > 0 {
		return errors.New("must be true")
	}
	return nil
}

// Emit adds the repository to advisory.list.
func (advisories) Emit(r *Resolved, out *Output) error {
	out.Files[FileAdvisory] = append(out.Files[FileAdvisory], r.Name)
	return nil
}
