package repos

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// FileSync is the file bin/sync reads: the clones it fast-forwards onto main.
const FileSync = "sync.map"

func init() { Register(syncFeature{}) }

// syncFeature is a plain extension: it adds nothing to the built-in outputs and
// owns one generated file of its own, through Output.Files.
type syncFeature struct{ base }

func (syncFeature) Key() string { return "sync" }
func (syncFeature) Doc() string {
	return "fast-forward a local clone onto origin/main (bin/sync): an absolute path, or true to reuse `checkout` (sync.map)"
}

func (syncFeature) Check(v any) error {
	switch x := v.(type) {
	case string:
		if strings.ContainsFunc(x, unicode.IsControl) {
			return fmt.Errorf("%q contains a control character; remove it", x)
		}
		if !strings.HasPrefix(x, "/") || strings.ContainsAny(x, "=") {
			return fmt.Errorf("%q is not an absolute path", x)
		}
	case map[string]any:
		if len(x) > 0 {
			return errors.New("must be true or an absolute path")
		}
	default:
		return errors.New("must be true or an absolute path")
	}
	return nil
}

func (syncFeature) Emit(r *Resolved, out *Output) error {
	path, ok := r.Values["sync"].(string)
	if !ok {
		path, _ = r.Values["checkout"].(string)
		if path == "" {
			return errors.New("true needs a `checkout` path to sync; give an absolute path instead")
		}
	}
	out.Files[FileSync] = append(out.Files[FileSync], r.Name+"="+path)
	return nil
}
