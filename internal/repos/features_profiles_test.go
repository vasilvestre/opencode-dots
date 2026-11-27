package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0035: one engine, two policy profiles. The example file ships both, and the same rules must
// come out with different policy and nothing else.
func TestExampleProfilesDifferOnlyByPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "repos.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Build(data, t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	rules := string(files[FileAwfGh])
	for _, want := range []string{"your-org/personal-tool", "autonomy: auto", "breaker: on"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the personal profile lost %q", want)
		}
	}
	// The company repository has its own watcher file because it names an executor.
	company := string(files[ExecutorFile("server")])
	for _, want := range []string{"company-org/service", "autonomy: act", "identity: marker", "budget: daily_usd=20"} {
		if !strings.Contains(company, want) {
			t.Errorf("the company profile lost %q:\n%s", want, company)
		}
	}
	if strings.Contains(company, "autonomy: auto") || strings.Contains(company, "breaker: on") {
		t.Errorf("the company profile must never reach auto or carry a breaker:\n%s", company)
	}
	tend := string(files[FileTend])
	if !strings.Contains(tend, "your-org/personal-tool true autonomy=auto") {
		t.Errorf("tend.list lost the personal line:\n%s", tend)
	}
}

// A company profile that asks for auto must be refused: trust client forbids it.
func TestCompanyProfileRefusesAuto(t *testing.T) {
	src := "version: 1\nsettings:\n  executors: {server: 'http://127.0.0.1:2512'}\n  trust:\n    host: client\n    region: eu\n    regions: {server: eu}\n    allow: {providers: [opencode], models: [], executors: [server]}\n" +
		"profiles:\n  company: {trust: client, autonomy: auto, identity: marker, review: true}\n" +
		"repos:\n  acme/svc: {use: [company], executor: server}\n"
	if _, err := Build([]byte(src), t.TempDir(), Settings{}); err == nil || !strings.Contains(err.Error(), "auto") {
		t.Fatalf("a client repository at auto must be refused, got %v", err)
	}
}
