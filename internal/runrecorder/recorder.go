// Package runrecorder keeps the event log of every awf execution in STATE_DIR/logs. awf forgets
// the events of a run once it leaves its session registry, so a recording has to start while the
// run is running: the recorder polls awf, copies each run's event stream to <id>.jsonl, keeps an
// index of what it recorded, prunes what is older than awf's own history, and says once when a
// run ended.
package runrecorder

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	retention   = 30 * 24 * time.Hour
	pruneEvery  = 6 * time.Hour
	indexFile   = "index.jsonl"
	indexLimit  = 8 << 20
	tailLimit   = 4096
	maxIDLength = 64
)

// Entry maps a recorded execution to the workflow and start time awf reports in its history.
// History ids differ from execution ids, so the page joins on workflow plus start second.
type Entry struct {
	ID           string `json:"execution_id"`
	WorkflowName string `json:"workflow_name"`
	StartedAt    string `json:"started_at"`
}

// Recorder records the executions of one awf into one state directory.
type Recorder struct {
	state, awf string
	notify     func(id, workflow, status string)

	mu       sync.Mutex
	active   map[string]bool // being recorded, retried until the run ends
	tried    map[string]bool // a finished run, fetched once
	notified map[string]bool // one message per execution id
	indexed  map[string]bool
}

// New builds a recorder. notify, if set, is called once per run whose log shows it completed or
// failed; the logs already on disk count as announced, so a restart repeats nothing.
func New(state, awf string, notify func(id, workflow, status string)) *Recorder {
	r := &Recorder{state: state, awf: awf, notify: notify,
		active: map[string]bool{}, tried: map[string]bool{}, notified: map[string]bool{}, indexed: map[string]bool{}}
	for _, e := range r.Entries() {
		r.indexed[e.ID] = true
	}
	entries, _ := os.ReadDir(r.dir())
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && !e.IsDir() && e.Name() != indexFile {
			r.notified[id] = true
		}
	}
	return r
}

func (r *Recorder) dir() string { return filepath.Join(r.state, "logs") }

// Path is where the log of an execution is, whether or not it exists.
func (r *Recorder) Path(id string) string { return filepath.Join(r.dir(), id+".jsonl") }

// ValidID says whether id can name a log file: it keeps an id from reaching the filesystem as a path.
func ValidID(id string) bool {
	if len(id) < 8 || len(id) > maxIDLength {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// Run records forever: every second it asks awf for its executions and starts recording the new
// ones, then calls tick; every six hours it prunes. It does not return.
func (r *Recorder) Run(tick func()) {
	lastPrune := time.Time{}
	for {
		r.poll()
		if tick != nil {
			tick()
		}
		if time.Since(lastPrune) > pruneEvery {
			r.Prune()
			lastPrune = time.Now()
		}
		time.Sleep(time.Second)
	}
}

// poll records every execution awf knows and has no log for yet. A run can be shorter than the
// poll interval, so a finished run is fetched once too: awf keeps its session for a while.
func (r *Recorder) poll() {
	var list struct {
		Body struct {
			Executions []struct {
				ID           string `json:"execution_id"`
				Status       string `json:"status"`
				WorkflowName string `json:"workflow_name"`
				StartedAt    string `json:"started_at"`
			} `json:"executions"`
		} `json:"body"`
	}
	resp, err := http.Get(r.awf + "/api/executions")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if json.NewDecoder(resp.Body).Decode(&list) != nil {
		return
	}
	for _, e := range list.Body.Executions {
		if !ValidID(e.ID) {
			continue
		}
		if fileExists(r.Path(e.ID)) {
			r.Note(e.ID, e.WorkflowName, e.StartedAt)
			r.announce(e.ID, e.WorkflowName)
			continue
		}
		r.mu.Lock()
		switch {
		case r.active[e.ID] || r.tried[e.ID]:
		case e.Status == "running":
			r.active[e.ID] = true
			go r.record(e.ID, e.WorkflowName, e.StartedAt, true)
		default:
			r.tried[e.ID] = true
			go r.record(e.ID, e.WorkflowName, e.StartedAt, false)
		}
		r.mu.Unlock()
	}
}

func (r *Recorder) record(id, workflow, started string, retry bool) {
	if retry {
		defer func() {
			r.mu.Lock()
			delete(r.active, id)
			r.mu.Unlock()
		}()
	}
	resp, err := http.Get(r.awf + "/api/executions/" + id + "/events")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || os.MkdirAll(r.dir(), 0o755) != nil {
		return
	}
	f, err := os.Create(r.Path(id))
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(f, resp.Body)
	// A session that already served its events returns an empty stream; leave no empty file
	// behind so the page says "no log" instead of a blank box.
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		os.Remove(r.Path(id))
		return
	}
	r.Note(id, workflow, started)
}

// Note adds a recorded execution to the index, once.
func (r *Recorder) Note(id, workflow, started string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.indexed[id] {
		return
	}
	r.indexed[id] = true
	line, _ := json.Marshal(Entry{ID: id, WorkflowName: workflow, StartedAt: started})
	f, err := os.OpenFile(filepath.Join(r.dir(), indexFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Entries is the index in the order it was written, each execution once.
func (r *Recorder) Entries() []Entry {
	var out []Entry
	seen := map[string]bool{}
	for line := range strings.SplitSeq(readCapped(filepath.Join(r.dir(), indexFile)), "\n") {
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" && !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

// Entry is the index line of one execution.
func (r *Recorder) Entry(id string) (Entry, bool) {
	for _, e := range r.Entries() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Has says whether an execution is in the index.
func (r *Recorder) Has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.indexed[id]
}

// IDs lists the indexed executions, in no order.
func (r *Recorder) IDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.indexed))
	for id := range r.indexed {
		ids = append(ids, id)
	}
	return ids
}

// Prune mirrors the 30 day retention awf keeps for its own history, and drops the index lines
// whose log is gone.
func (r *Recorder) Prune() {
	entries, err := os.ReadDir(r.dir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-retention)
	live := map[string]bool{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(r.dir(), e.Name()))
			continue
		}
		live[e.Name()] = true
	}
	var keep strings.Builder
	indexed := map[string]bool{}
	for line := range strings.SplitSeq(readCapped(filepath.Join(r.dir(), indexFile)), "\n") {
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil || !live[e.ID+".jsonl"] {
			continue
		}
		keep.WriteString(line + "\n")
		indexed[e.ID] = true
	}
	_ = os.WriteFile(filepath.Join(r.dir(), indexFile), []byte(keep.String()), 0o644)
	r.mu.Lock()
	r.indexed = indexed
	for id := range r.notified {
		if !live[id+".jsonl"] {
			delete(r.notified, id)
		}
	}
	r.mu.Unlock()
}

// Terminal is the end the recorder saw for an execution: "completed", "failed", or "" while it
// has not ended. Terminal events are written last, so the tail of the file is enough.
func (r *Recorder) Terminal(id string) string {
	b, err := os.ReadFile(r.Path(id))
	if err != nil {
		return ""
	}
	tail := string(b[max(0, len(b)-tailLimit):])
	for _, status := range []string{"completed", "failed"} {
		if strings.Contains(tail, `"kind":"workflow.`+status+`"`) {
			return status
		}
	}
	return ""
}

// Ended says whether the recorder already saw the end of the run, of any kind (a cancelled run
// has its own terminal event).
func (r *Recorder) Ended(id string) bool {
	b, err := os.ReadFile(r.Path(id))
	if err != nil {
		return false
	}
	return strings.Contains(string(b[max(0, len(b)-tailLimit):]), `"kind":"workflow.`)
}

// announce calls notify once for a finished run. Each execution id is considered once per process.
func (r *Recorder) announce(id, workflow string) {
	if r.notify == nil {
		return
	}
	r.mu.Lock()
	seen := r.notified[id]
	r.mu.Unlock()
	if seen {
		return
	}
	status := r.Terminal(id)
	if status == "" {
		return
	}
	r.mu.Lock()
	if r.notified[id] {
		r.mu.Unlock()
		return
	}
	r.notified[id] = true
	r.mu.Unlock()
	r.notify(id, workflow, status)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readCapped(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, indexLimit)
	n, _ := f.Read(buf)
	return string(buf[:n])
}
