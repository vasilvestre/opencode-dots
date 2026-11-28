// Package statedir names the one place every run artifact lives.
package statedir

import (
	"cmp"
	"os"
	"path/filepath"
)

// Default is $STATE_DIR, else <root>/state.
func Default(root string) string {
	return cmp.Or(os.Getenv("STATE_DIR"), filepath.Join(root, "state"))
}
