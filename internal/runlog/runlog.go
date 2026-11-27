// Package runlog reads the record of a run for people and agents who debug it:
// the run index, the per-run event log the dashboard records under state/logs,
// and awf's own transcript when it exists. It only reads. The dashboard keeps
// its own readers; this package is what the dots command uses.
//
// Two line shapes exist and both are accepted: the recorder writes SSE frames
// ("data: {...}", step name under "step"), awf's transcript writes bare JSON
// with a timestamp and the step under "payload".
package runlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"opencode-dots/internal/spend"
)

// Event is one line of a run, whichever file it came from.
type Event struct {
	Kind   string  `json:"kind"`
	Seq    uint64  `json:"seq,omitempty"`
	At     string  `json:"at,omitempty"`
	Step   string  `json:"step,omitempty"`
	Status string  `json:"status,omitempty"`
	Error  string  `json:"error,omitempty"`
	Cost   float64 `json:"cost_usd,omitempty"`
	Text   string  `json:"text,omitempty"`
}

// Step is what a run did at one step.
type Step struct {
	Name   string  `json:"name"`
	Status string  `json:"status"`
	MS     int64   `json:"ms,omitempty"`
	Cost   float64 `json:"cost_usd,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Run is one execution. Status is completed, failed or running, read from the
// events, not from the index, which holds no status.
type Run struct {
	ID       string  `json:"id"`
	Workflow string  `json:"workflow"`
	Started  string  `json:"started"`
	Status   string  `json:"status"`
	Error    string  `json:"error,omitempty"`
	Failed   string  `json:"failed_step,omitempty"`
	Cost     float64 `json:"cost_usd,omitempty"`
	Steps    []Step  `json:"steps,omitempty"`
}

type rawLine struct {
	Kind    string  `json:"kind"`
	Type    string  `json:"type"`
	Seq     uint64  `json:"seq"`
	Time    string  `json:"timestamp"`
	Step    string  `json:"step"`
	Status  string  `json:"status"`
	Error   string  `json:"error"`
	CostUSD float64 `json:"cost_usd"`
	Payload struct {
		Name    string  `json:"name"`
		Status  string  `json:"status"`
		Error   string  `json:"error"`
		CostUSD float64 `json:"cost_usd"`
		Blocks  []struct {
			Text string `json:"text"`
		} `json:"blocks"`
	} `json:"payload"`
}

// ParseLine reads one line of either shape. ok is false for blank lines and
// for lines that are not an event.
func ParseLine(line string) (Event, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
	if line == "" || line[0] != '{' {
		return Event{}, false
	}
	var r rawLine
	if json.Unmarshal([]byte(line), &r) != nil {
		return Event{}, false
	}
	e := Event{Kind: r.Kind, Seq: r.Seq, At: r.Time, Step: r.Step, Status: r.Status, Error: r.Error, Cost: r.CostUSD}
	if e.Kind == "" {
		e.Kind = r.Type
	}
	if e.Kind == "" {
		return Event{}, false
	}
	p := r.Payload
	if e.Step == "" {
		e.Step = p.Name
	}
	if e.Status == "" {
		e.Status = p.Status
	}
	if e.Error == "" {
		e.Error = p.Error
	}
	if e.Cost == 0 {
		e.Cost = p.CostUSD
	}
	var text []string
	for _, b := range p.Blocks {
		text = append(text, b.Text)
	}
	e.Text = strings.Join(text, "\n")
	return e, true
}

// Store points at a state directory.
type Store struct {
	State string
	Dirs  []string // transcript directories; defaults to spend.TranscriptDirs
}

// New returns a Store for the state directory.
func New(state string) *Store {
	return &Store{State: state, Dirs: spend.TranscriptDirs(state)}
}

type indexLine struct {
	ID       string `json:"execution_id"`
	Workflow string `json:"workflow_name"`
	Started  string `json:"started_at"`
}

func (s *Store) index() []indexLine {
	f, err := os.Open(filepath.Join(s.State, "logs", "index.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []indexLine
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var l indexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.ID == "" || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		out = append(out, l)
	}
	// started_at carries mixed zones: sort on the instant, not the text.
	sort.SliceStable(out, func(i, j int) bool { return instant(out[i].Started).After(instant(out[j].Started)) })
	return out
}

func instant(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Path returns the file holding a run's events: the transcript first, since it
// has timestamps and agent messages, then the recorder file.
func (s *Store) Path(id string) string {
	for _, d := range s.Dirs {
		if p := filepath.Join(d, id+".jsonl"); fileExists(p) {
			return p
		}
	}
	if p := filepath.Join(s.State, "logs", id+".jsonl"); fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Resolve turns an id, a unique id prefix or "last" into a full run id.
func (s *Store) Resolve(ref string) (string, error) {
	idx := s.index()
	if ref == "last" {
		if len(idx) == 0 {
			return "", errors.New("no run recorded")
		}
		return idx[0].ID, nil
	}
	var hits []string
	for _, l := range idx {
		if l.ID == ref {
			return l.ID, nil
		}
		if strings.HasPrefix(l.ID, ref) {
			hits = append(hits, l.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no run matches %q", ref)
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("%q matches %d runs, give more characters", ref, len(hits))
}

// Events reads every event of a run.
func (s *Store) Events(id string) ([]Event, error) {
	p := s.Path(id)
	if p == "" {
		return nil, fmt.Errorf("no log for run %s", id)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if e, ok := ParseLine(sc.Text()); ok {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Summarize folds events into a Run. A step that started and never ended in a
// run whose workflow did end is reported as it was left, "running".
func Summarize(id, workflow, started string, events []Event) Run {
	run := Run{ID: id, Workflow: workflow, Started: started, Status: "running"}
	open := map[string]*Step{}
	var order []*Step
	starts := map[string]time.Time{}
	for _, e := range events {
		switch e.Kind {
		case "step.started":
			st := &Step{Name: e.Step, Status: "running"}
			order = append(order, st)
			open[e.Step] = st
			starts[e.Step] = instant(e.At)
		case "step.completed", "step.failed":
			st := open[e.Step]
			if st == nil {
				st = &Step{Name: e.Step}
				order = append(order, st)
			}
			st.Status, st.Error, st.Cost = e.Status, e.Error, e.Cost
			if st.Status == "" {
				st.Status = strings.TrimPrefix(e.Kind, "step.")
			}
			if t0, end := starts[e.Step], instant(e.At); !t0.IsZero() && !end.IsZero() {
				st.MS = end.Sub(t0).Milliseconds()
			}
			delete(open, e.Step)
			// The cause is the first step to fail in event order. Start order
			// would name the wrapper (rework) that fails after its child (review).
			if st.Status == "failed" && run.Failed == "" {
				run.Failed = st.Name
			}
		case "workflow.completed":
			run.Status = "completed"
		case "workflow.failed":
			run.Status, run.Error = "failed", e.Error
		}
	}
	for _, st := range order {
		run.Steps = append(run.Steps, *st)
		run.Cost += st.Cost
	}
	if run.Failed != "" && run.Status == "running" {
		run.Status = "failed"
	}
	return run
}

// Get loads one run with its steps.
func (s *Store) Get(ref string) (Run, []Event, error) {
	id, err := s.Resolve(ref)
	if err != nil {
		return Run{}, nil, err
	}
	ev, err := s.Events(id)
	if err != nil {
		return Run{}, nil, err
	}
	var wf, started string
	for _, l := range s.index() {
		if l.ID == id {
			wf, started = l.Workflow, l.Started
		}
	}
	return Summarize(id, wf, started, ev), ev, nil
}

// Filter narrows List.
type Filter struct {
	Workflow string
	Failed   bool
	Limit    int
}

// List returns the newest runs first, without steps.
func (s *Store) List(f Filter) []Run {
	var out []Run
	for _, l := range s.index() {
		if f.Workflow != "" && l.Workflow != f.Workflow {
			continue
		}
		ev, err := s.Events(l.ID)
		run := Run{ID: l.ID, Workflow: l.Workflow, Started: l.Started, Status: "no log"}
		if err == nil {
			run = Summarize(l.ID, l.Workflow, l.Started, ev)
			run.Steps = nil
		}
		if f.Failed && run.Status != "failed" {
			continue
		}
		out = append(out, run)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}
