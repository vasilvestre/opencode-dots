package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// loopToolCalls is the tool-call count above which a session is flagged as
// looping. The session that hit a 10 minute timeout had 641.
const loopToolCalls = 150

// sessionSlack widens a step's window: opencode stamps a session a moment
// before or after awf stamps the step.
const sessionSlack = 5 * time.Second

// sessionRow is one opencode session as stored in session_v2, plus the number
// of tool parts it holds. Times are epoch milliseconds; Idle is 0 while the
// session is busy.
type sessionRow struct {
	ID      string `json:"id"`
	Dir     string `json:"directory"`
	Model   string `json:"model"`
	Out     int64  `json:"tokens_output"`
	Created int64  `json:"time_created"`
	Updated int64  `json:"time_updated"`
	Idle    int64  `json:"time_idle"`
	Tools   int    `json:"tools"`
}

// stepSession is what the run view shows for one agent step.
type stepSession struct {
	Step   string `json:"step"`
	ID     string `json:"id"`
	Model  string `json:"model,omitempty"`
	Tools  int    `json:"tool_calls"`
	Output int64  `json:"output_tokens"`
	Orphan bool   `json:"orphan,omitempty"`
	Loop   bool   `json:"loop,omitempty"`
}

// stepWindow is when a step ran. End is zero while the step is still open.
type stepWindow struct {
	Name       string
	Start, End time.Time
}

// correlateSessions attaches to each step the sessions that started inside its
// window. A session is an orphan when its step has ended and it is still busy
// (no time_idle at or after its last update), and a loop when it made more than
// loopToolCalls tool calls. The match is a time guess, not an id.
func correlateSessions(steps []stepWindow, rows []sessionRow) []stepSession {
	out := []stepSession{}
	for _, st := range steps {
		for _, r := range rows {
			at := time.UnixMilli(r.Created)
			if at.Before(st.Start.Add(-sessionSlack)) {
				continue
			}
			if !st.End.IsZero() && at.After(st.End.Add(sessionSlack)) {
				continue
			}
			out = append(out, stepSession{
				Step:   st.Name,
				ID:     r.ID,
				Model:  modelLabel(r.Model),
				Tools:  r.Tools,
				Output: r.Out,
				Orphan: !st.End.IsZero() && (r.Idle == 0 || r.Idle < r.Updated),
				Loop:   r.Tools > loopToolCalls,
			})
		}
	}
	return out
}

// modelLabel turns the model JSON {"id","providerID"} into provider/id.
func modelLabel(raw string) string {
	var m struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil || m.ID == "" {
		return ""
	}
	if m.Provider == "" {
		return m.ID
	}
	return m.Provider + "/" + m.ID
}

// querySessions reads sessions under dir created in [from, to] (epoch ms) from
// the opencode database, read-only, through the sqlite3 command so the module
// needs no driver. A missing database or command is an error the caller hides.
func querySessions(ctx context.Context, db, dir string, from, to int64) ([]sessionRow, error) {
	if !exists(db) {
		return nil, fmt.Errorf("no opencode database at %s", db)
	}
	eq := strings.ReplaceAll(dir, "'", "''")
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(eq)
	q := fmt.Sprintf(`SELECT s.id, s.directory, COALESCE(s.model,'') AS model,
  s.tokens_output, s.time_created, s.time_updated, COALESCE(s.time_idle,0) AS time_idle,
  (SELECT count(*) FROM session_part p WHERE p.session_id = s.id AND json_extract(p.data,'$.type') = 'tool') AS tools
FROM session_v2 s
WHERE (s.directory = '%s' OR s.directory LIKE '%s/%%' ESCAPE '\')
  AND s.time_created BETWEEN %d AND %d
ORDER BY s.time_created`, eq, like, from, to)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", db, q).Output()
	if err != nil {
		return nil, err
	}
	var rows []sessionRow
	if len(strings.TrimSpace(string(out))) == 0 {
		return rows, nil
	}
	return rows, json.Unmarshal(out, &rows)
}

// stepWindows reads the agent steps of a run out of its transcript.
func stepWindows(transcript string) []stepWindow {
	var steps []stepWindow
	open := map[string]int{}
	for _, line := range strings.Split(readFile(transcript, 8<<20), "\n") {
		var e transcriptEvent
		if line == "" || json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		switch e.Type {
		case "step.started":
			if e.Payload.Kind == "agent" {
				open[e.Payload.Name] = len(steps)
				steps = append(steps, stepWindow{Name: e.Payload.Name, Start: at})
			}
		case "step.completed", "step.failed":
			if i, ok := open[e.Payload.Name]; ok {
				steps[i].End = at
				delete(open, e.Payload.Name)
			}
		}
	}
	return steps
}

// sessions answers /api/sessions?id=: the opencode session behind each agent
// step of a run. It adds nothing when the database or transcript is missing.
func (d *dash) sessions(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		writeJSONStatus(w, 400, map[string]string{"error": "bad id"})
		return
	}
	res := map[string]any{"id": id, "sessions": []stepSession{}}
	path := d.transcriptPath(id)
	if path == "" || d.opencodeDB == "" {
		writeJSON(w, res)
		return
	}
	steps := stepWindows(path)
	if len(steps) == 0 {
		writeJSON(w, res)
		return
	}
	from := steps[0].Start
	rows, err := querySessions(r.Context(), d.opencodeDB, filepath.Clean(d.state),
		from.Add(-sessionSlack).UnixMilli(), time.Now().Add(time.Minute).UnixMilli())
	if err != nil {
		writeJSON(w, res)
		return
	}
	res["sessions"] = correlateSessions(steps, rows)
	writeJSON(w, res)
}

// defaultOpencodeDB is where opencode keeps its database.
func defaultOpencodeDB() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "opencode", "opencode.db")
}
