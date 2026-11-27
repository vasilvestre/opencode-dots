package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesWholeAndLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	for _, content := range []string{"first", "second, longer"} {
		if err := Write(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Fatalf("content = %q", got)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
	if err := Write(filepath.Join(dir, "missing", "f"), nil, 0o600); err == nil {
		t.Fatal("a missing directory was accepted")
	}
}
