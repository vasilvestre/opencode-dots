// Package atomicfile writes a file so that a reader, or a crash, sees the old content or the new
// one and never half of it.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write puts data at path with the given mode: a temporary file in the same directory, synced,
// then renamed over the target. The directory must exist.
func Write(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), perm)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
