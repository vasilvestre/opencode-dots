package repos

import "testing"

func TestSandboxTestKey(t *testing.T) {
	cases := []struct {
		name string
		test map[string]any
		ok   bool
	}{
		{"image", map[string]any{"image": "dots-test", "command": "make test"}, true},
		{"dockerfile", map[string]any{"dockerfile": "Dockerfile", "command": "make test"}, true},
		{"dockerfile in a folder", map[string]any{"dockerfile": "ci/Dockerfile.test", "command": "make test"}, true},
		{"dockerfile with a target", map[string]any{"dockerfile": "Dockerfile", "target": "dev", "command": "make test"}, true},
		{"target without a dockerfile", map[string]any{"image": "dots-test", "target": "dev", "command": "true"}, false},
		{"target that is not a stage name", map[string]any{"dockerfile": "Dockerfile", "target": "dev; rm", "command": "true"}, false},
		{"empty target", map[string]any{"dockerfile": "Dockerfile", "target": "", "command": "true"}, false},
		{"both", map[string]any{"image": "dots-test", "dockerfile": "Dockerfile", "command": "make test"}, false},
		{"neither", map[string]any{"command": "make test"}, false},
		{"image outside the prefix", map[string]any{"image": "alpine", "command": "true"}, false},
		{"absolute dockerfile", map[string]any{"dockerfile": "/etc/Dockerfile", "command": "true"}, false},
		{"dockerfile leaving the repository", map[string]any{"dockerfile": "../Dockerfile", "command": "true"}, false},
		{"dockerfile with a space", map[string]any{"dockerfile": "my Dockerfile", "command": "true"}, false},
		{"no command", map[string]any{"dockerfile": "Dockerfile"}, false},
		{"unknown key", map[string]any{"dockerfile": "Dockerfile", "command": "true", "network": "on"}, false},
	}
	for _, c := range cases {
		err := sandbox{}.Check(map[string]any{"test": c.test})
		if (err == nil) != c.ok {
			t.Errorf("%s: got %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
