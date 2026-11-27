// Command health answers one question: is the dot alive? It checks the awf API,
// the executors of repos.yaml, the GitHub token, the project's systemd user
// units, the state directory and the age of the last run.
//
// Exit codes: 0 healthy, 1 degraded, 2 down. See docs/health.md.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"opencode-dots/internal/repos"
)

func main() {
	root, _ := os.Getwd()
	dir := flag.String("dir", root, "repository root: holds repos.yaml and .env")
	state := flag.String("state", envOr("STATE_DIR", filepath.Join(root, "state")), "state directory")
	awf := flag.String("awf", envOr("AWF_URL", "http://127.0.0.1:2511"), "awf API base URL")
	asJSON := flag.Bool("json", false, "print a JSON report")
	minFree := flag.Uint64("min-free-mb", 1024, "disk free threshold, in MiB")
	warnDays := flag.Int("token-warn-days", 7, "warn when the token expires within this many days")
	stale := flag.Duration("stale-after", 48*time.Hour, "warn when the last run is older than this")
	units := flag.String("units", "dots-awf.service,dots-dashboard.service,dots-opencode.service,dots-awf-gh.timer,dots-tend.timer", "systemd user units to check when enabled")
	flag.Parse()

	token := ghToken(*dir)
	cfg := Config{
		AwfURL: *awf, StateDir: *state, MinFree: *minFree << 20, WarnDays: *warnDays,
		StaleAfter: *stale, Now: time.Now(), HaveGHToken: token != "",
		Units: strings.Split(*units, ","), Executors: map[string]string{},
	}
	if doc, err := repos.Load(filepath.Join(*dir, "repos.yaml")); err == nil {
		cfg.Executors = doc.Settings.Executors
	}
	client := &http.Client{Timeout: 5 * time.Second}
	rep := Run(cfg, Deps{
		Probe: probe(client), Token: githubToken(client, token),
		UnitsAvailable: unitsAvailable, UnitState: unitState,
		Writable: writable, FreeBytes: freeBytes, LatestRun: latestRun,
	})
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(rep)
	} else {
		fmt.Print(rep.Text())
	}
	os.Exit(rep.ExitCode())
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ghToken reads GH_TOKEN from the environment, then from .env, as the units do.
func ghToken(dir string) string {
	if v := os.Getenv("GH_TOKEN"); v != "" {
		return v
	}
	f, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "GH_TOKEN="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func probe(c *http.Client) func(string) error {
	return func(url string) error {
		resp, err := c.Get(url)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}
}

func githubToken(c *http.Client, token string) func() (bool, time.Time, error) {
	return func() (bool, time.Time, error) {
		req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := c.Do(req)
		if err != nil {
			// net/http errors carry the URL, never the header.
			return false, time.Time{}, err
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return false, time.Time{}, nil
		}
		if resp.StatusCode >= 500 {
			return false, time.Time{}, fmt.Errorf("status %d", resp.StatusCode)
		}
		return true, parseExpiry(resp.Header.Get("github-authentication-token-expiration")), nil
	}
}

// parseExpiry reads "2026-12-01 10:00:00 UTC"; empty or unparsable gives zero.
func parseExpiry(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05 MST", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

func unitsAvailable() bool {
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func unitState(unit string) (string, bool) {
	out, err := exec.Command("systemctl", "--user", "show", unit, "-p", "LoadState,ActiveState,UnitFileState").Output()
	if err != nil {
		return "", false
	}
	p := map[string]string{}
	for l := range strings.SplitSeq(string(out), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			p[k] = v
		}
	}
	if p["LoadState"] != "loaded" || p["UnitFileState"] == "disabled" {
		return "", false
	}
	return p["ActiveState"], true
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".health-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

func freeBytes(dir string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0, err
	}
	return s.Bavail * uint64(s.Bsize), nil
}

// latestRun takes the newest file under the run directories of state/.
func latestRun(dir string) (time.Time, bool, error) {
	var newest time.Time
	for _, sub := range []string{"reviews", "replies", "tend"} {
		_ = filepath.WalkDir(filepath.Join(dir, sub), func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil //nolint:nilerr // an unreadable path is skipped, not fatal
			}
			if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
	}
	return newest, !newest.IsZero(), nil
}
