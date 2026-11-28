package statedir

import "testing"

func TestDefault(t *testing.T) {
	t.Setenv("STATE_DIR", "")
	if got := Default("/r"); got != "/r/state" {
		t.Fatal(got)
	}
	t.Setenv("STATE_DIR", "/x")
	if got := Default("/r"); got != "/x" {
		t.Fatal(got)
	}
}
