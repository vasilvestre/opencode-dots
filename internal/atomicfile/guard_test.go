package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// State files are written through Write. A bare os.WriteFile is allowed only where listed, with the reason.
var allowed = map[string]string{
	"cmd/dashboard/files.go": "a new .bak copy, never read while written",
}

func TestNoBareWriteFile(t *testing.T) {
	root := "../.."
	filepath.WalkDir(root, func(p string, d fs.DirEntry, _ error) error {
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") ||
			(!strings.HasPrefix(rel, "cmd/") && !strings.HasPrefix(rel, "internal/")) || strings.HasPrefix(rel, "internal/atomicfile/") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if _, ok := allowed[rel]; !ok && strings.Contains(string(b), "os.WriteFile(") {
			t.Errorf("%s: use atomicfile.Write for state files, or list it in allowed with a reason", rel)
		}
		return nil
	})
}
