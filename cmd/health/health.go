package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Status of one check. Skip means the check does not apply here.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Result is what one check reports. Critical marks a failure that stops the dot
// (exit 2); any other failure or warning only degrades it (exit 1).
type Result struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Detail   string `json:"detail"`
	Critical bool   `json:"critical,omitempty"`
}

// Report is the whole run.
type Report struct {
	Overall string   `json:"overall"` // healthy, degraded or down
	Checks  []Result `json:"checks"`
}

// Exit codes.
const (
	ExitHealthy  = 0
	ExitDegraded = 1
	ExitDown     = 2
)

// Deps are the effects, replaced by fakes in tests.
type Deps struct {
	// Probe asks an HTTP endpoint whether it answers.
	Probe func(url string) error
	// Token returns whether the GitHub token is valid and, when GitHub reports one, its expiry.
	Token func() (valid bool, expires time.Time, err error)
	// UnitsAvailable is false when systemd is not usable here.
	UnitsAvailable func() bool
	// UnitState returns ("", false) when the unit is not installed or not enabled.
	UnitState func(unit string) (active string, installed bool)
	// Writable checks the state directory can be written.
	Writable func(dir string) error
	// FreeBytes reports free disk space under dir.
	FreeBytes func(dir string) (uint64, error)
	// LatestRun finds the newest run artifact under the state directory.
	LatestRun func(dir string) (time.Time, bool, error)
}

// Config says what to check.
type Config struct {
	AwfURL      string
	Executors   map[string]string
	StateDir    string
	Units       []string
	MinFree     uint64
	WarnDays    int
	StaleAfter  time.Duration
	Now         time.Time
	HaveGHToken bool // false: no token is configured at all
}

// Run performs every check and sums them up.
func Run(c Config, d Deps) Report {
	var rs []Result
	rs = append(rs, checkAwf(c, d))
	rs = append(rs, checkExecutors(c, d)...)
	rs = append(rs, checkToken(c, d))
	rs = append(rs, checkUnits(c, d)...)
	rs = append(rs, checkState(c, d)...)
	rs = append(rs, checkLastRun(c, d))
	return Report{Overall: overall(rs), Checks: rs}
}

func overall(rs []Result) string {
	o := "healthy"
	for _, r := range rs {
		switch {
		case r.Status == Fail && r.Critical:
			return "down"
		case r.Status == Fail || r.Status == Warn:
			o = "degraded"
		}
	}
	return o
}

// ExitCode maps the report to 0, 1 or 2.
func (r Report) ExitCode() int {
	switch r.Overall {
	case "down":
		return ExitDown
	case "degraded":
		return ExitDegraded
	}
	return ExitHealthy
}

func checkAwf(c Config, d Deps) Result {
	r := Result{Name: "awf", Critical: true}
	if err := d.Probe(strings.TrimRight(c.AwfURL, "/") + "/api/workflows"); err != nil {
		r.Status, r.Detail = Fail, fmt.Sprintf("%s does not answer: %v", c.AwfURL, err)
		return r
	}
	r.Status, r.Detail = OK, c.AwfURL+" answers"
	return r
}

func checkExecutors(c Config, d Deps) []Result {
	names := make([]string, 0, len(c.Executors))
	for n := range c.Executors {
		names = append(names, n)
	}
	slices.Sort(names)
	var rs []Result
	for _, n := range names {
		u := c.Executors[n]
		r := Result{Name: "executor " + n}
		if err := d.Probe(strings.TrimRight(u, "/") + "/api/workflows"); err != nil {
			r.Status, r.Detail = Fail, fmt.Sprintf("%s does not answer: %v", u, err)
		} else {
			r.Status, r.Detail = OK, u+" answers"
		}
		rs = append(rs, r)
	}
	return rs
}

func checkToken(c Config, d Deps) Result {
	r := Result{Name: "github token", Critical: true}
	if !c.HaveGHToken {
		r.Status, r.Detail = Fail, "no GH_TOKEN in the environment or .env"
		return r
	}
	valid, exp, err := d.Token()
	switch {
	case err != nil:
		// GitHub unreachable says nothing about the token.
		r.Status, r.Critical, r.Detail = Warn, false, "cannot reach GitHub: "+err.Error()
	case !valid:
		r.Status, r.Detail = Fail, "GitHub refuses the token (expired or revoked)"
	case exp.IsZero():
		r.Status, r.Detail = OK, "valid, no expiry reported"
	default:
		days := int(exp.Sub(c.Now).Hours() / 24)
		switch {
		case exp.Before(c.Now):
			r.Status, r.Detail = Fail, "expired "+exp.Format("2006-01-02")
		case days < c.WarnDays:
			r.Status, r.Critical = Warn, false
			r.Detail = fmt.Sprintf("valid, expires in %d days (%s)", days, exp.Format("2006-01-02"))
		default:
			r.Status, r.Detail = OK, fmt.Sprintf("valid, expires in %d days", days)
		}
	}
	return r
}

func checkUnits(c Config, d Deps) []Result {
	if !d.UnitsAvailable() {
		return []Result{{Name: "units", Status: Skip, Detail: "systemd user session not available"}}
	}
	var rs []Result
	for _, u := range c.Units {
		state, installed := d.UnitState(u)
		if !installed {
			continue
		}
		r := Result{Name: "unit " + u}
		if state == "active" {
			r.Status, r.Detail = OK, "active"
		} else {
			r.Status, r.Detail = Fail, "is "+state
		}
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		rs = append(rs, Result{Name: "units", Status: Skip, Detail: "no unit of this project is enabled"})
	}
	return rs
}

func checkState(c Config, d Deps) []Result {
	w := Result{Name: "state dir", Critical: true}
	if err := d.Writable(c.StateDir); err != nil {
		w.Status, w.Detail = Fail, fmt.Sprintf("%s is not writable: %v", c.StateDir, err)
	} else {
		w.Status, w.Detail = OK, c.StateDir+" is writable"
	}
	f := Result{Name: "disk free"}
	free, err := d.FreeBytes(c.StateDir)
	switch {
	case err != nil:
		f.Status, f.Detail = Warn, "cannot read: "+err.Error()
	case free < c.MinFree:
		f.Status, f.Critical = Fail, true
		f.Detail = fmt.Sprintf("%s free, below %s", human(free), human(c.MinFree))
	default:
		f.Status, f.Detail = OK, human(free)+" free"
	}
	return []Result{w, f}
}

func checkLastRun(c Config, d Deps) Result {
	r := Result{Name: "last run"}
	t, found, err := d.LatestRun(c.StateDir)
	switch {
	case err != nil:
		r.Status, r.Detail = Warn, "cannot read: "+err.Error()
	case !found:
		r.Status, r.Detail = Warn, "no run recorded yet"
	default:
		age := c.Now.Sub(t)
		if age > c.StaleAfter {
			r.Status = Warn
		} else {
			r.Status = OK
		}
		r.Detail = fmt.Sprintf("%s ago", age.Round(time.Minute))
	}
	return r
}

func human(b uint64) string {
	const g = 1 << 30
	if b >= g {
		return fmt.Sprintf("%.1f GiB", float64(b)/g)
	}
	return fmt.Sprintf("%d MiB", b>>20)
}

// Text renders the human summary.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dot: %s\n", r.Overall)
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  %-5s %-34s %s\n", c.Status, c.Name, c.Detail)
	}
	return b.String()
}
