package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The Ask chat: one conversation per pull request, stored beside the run
// artifacts. A message becomes a run of the ask-pr workflow, whose answer.md
// comes back here as the next assistant message.
type threadMessage struct {
	Role    string       `json:"role"`
	Text    string       `json:"text"`
	At      string       `json:"at"`
	RunID   string       `json:"run_id,omitempty"`
	Cost    float64      `json:"cost,omitempty"`
	Suggest []suggestion `json:"suggest,omitempty"`
}

// suggestion is a workflow the agent named as the next step. The page turns
// each one into a button; nothing runs until the operator clicks it.
type suggestion struct {
	Workflow string            `json:"workflow"`
	Inputs   map[string]string `json:"inputs,omitempty"`
}

var (
	suggestRE  = regexp.MustCompile(`^SUGGEST:\s*(\S*)\s*(.*)$`)
	suggestKV  = regexp.MustCompile(`([A-Za-z0-9_]+)=(?:"([^"]*)"|(\S+))`)
	suggestMax = 3
)

// parseSuggest splits the answer into the text a reader sees and the workflows
// it proposes. The agent writes one line per suggestion, at the end:
//
//	SUGGEST: reply-pr repo=owner/repo pr=42
func parseSuggest(text string) (string, []suggestion) {
	kept := []string{}
	var out []suggestion
	for _, line := range strings.Split(text, "\n") {
		m := suggestRE.FindStringSubmatch(line)
		if m == nil {
			kept = append(kept, line)
			continue
		}
		// The line never reaches the reader, even when its workflow name is
		// refused: a suggestion the page cannot render is noise in the answer.
		if len(out) >= suggestMax || !workflowNameRE.MatchString(m[1]) {
			continue
		}
		inputs := map[string]string{}
		for _, kv := range suggestKV.FindAllStringSubmatch(m[2], -1) {
			v := kv[2]
			if v == "" {
				v = kv[3]
			}
			inputs[kv[1]] = v
		}
		if len(inputs) == 0 {
			inputs = nil
		}
		out = append(out, suggestion{Workflow: m[1], Inputs: inputs})
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), out
}

type thread struct {
	Repo     string          `json:"repo"`
	PR       string          `json:"pr"`
	Messages []threadMessage `json:"messages"`
}

var (
	asksMu        sync.Mutex
	prNumberRE    = regexp.MustCompile(`^[0-9]{1,7}$`)
	askMaxSize    = 16 << 10
	askQuestionTo = 8000
)

// prNumber reads the pull request number out of an artifact directory name
// such as "pr447" or "pr447-<sha>".
func prNumber(dir string) string {
	base := strings.TrimPrefix(dir, "pr")
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i]
	}
	if prNumberRE.MatchString(base) {
		return base
	}
	return ""
}

// repoFromURL keeps "owner/repo" out of a pull request URL.
func repoFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[2-1] == "" {
		return ""
	}
	repo := parts[0] + "/" + parts[1]
	if !repoNameRE.MatchString(repo) {
		return ""
	}
	return repo
}

func (d *dash) askDir(repo, pr string) string {
	return filepath.Join(d.state, "asks", filepath.FromSlash(repo), "pr"+pr)
}

func (d *dash) loadThread(repo, pr string) thread {
	out := thread{Repo: repo, PR: pr, Messages: []threadMessage{}}
	loadJSON(filepath.Join(d.askDir(repo, pr), "thread.json"), &out)
	if out.Messages == nil {
		out.Messages = []threadMessage{}
	}
	out.Repo, out.PR = repo, pr
	return out
}

// writeThread stores the conversation twice: thread.json for the page and
// thread.md for the agent, which reads markdown with its read tool.
func (d *dash) writeThread(t thread) error {
	dir := d.askDir(t.Repo, t.PR)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(thread{Repo: t.Repo, PR: t.PR, Messages: t.Messages}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "thread.json"), append(b, '\n')); err != nil {
		return err
	}
	var md strings.Builder
	md.WriteString("# Conversation about " + t.Repo + "#" + t.PR + "\n")
	for _, m := range t.Messages {
		md.WriteString("\n## " + m.Role + " · " + m.At + "\n\n" + m.Text + "\n")
	}
	return atomicWrite(filepath.Join(dir, "thread.md"), []byte(md.String()))
}

func (d *dash) thread(w http.ResponseWriter, r *http.Request) {
	repo, pr := r.URL.Query().Get("repo"), r.URL.Query().Get("pr")
	if !repoNameRE.MatchString(repo) || !prNumberRE.MatchString(pr) {
		writeJSONStatus(w, 400, map[string]string{"error": "repo and pr are required"})
		return
	}
	writeJSON(w, d.loadThread(repo, pr))
}

// startRun posts a run request to awf and returns the execution id.
func (d *dash) startRun(workflow string, inputs map[string]string) (string, error) {
	body, err := json.Marshal(map[string]any{"inputs": inputs})
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	u := d.awf + "/api/workflows/" + url.PathEscape(d.scopeOf(workflow)) + "/" + url.PathEscape(workflow) + "/run"
	resp, err := client.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("awf refused the run: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		Body struct {
			ExecutionID string `json:"execution_id"`
		} `json:"body"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Body.ExecutionID == "" {
		return "", fmt.Errorf("awf accepted the run but named no execution")
	}
	return out.Body.ExecutionID, nil
}

func (d *dash) ask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, 405, map[string]string{"error": "POST only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, 403, map[string]string{"error": "loopback only"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONStatus(w, 415, map[string]string{"error": "JSON only"})
		return
	}
	var in struct {
		Repo     string `json:"repo"`
		PR       string `json:"pr"`
		Question string `json:"question"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(askMaxSize))).Decode(&in) != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "unreadable body"})
		return
	}
	in.Question = strings.TrimSpace(in.Question)
	if !repoNameRE.MatchString(in.Repo) || !prNumberRE.MatchString(in.PR) || in.Question == "" {
		writeJSONStatus(w, 422, map[string]string{"error": "repo, pr and question are required"})
		return
	}
	if len(in.Question) > askQuestionTo {
		writeJSONStatus(w, 413, map[string]string{"error": fmt.Sprintf("question is longer than %d characters", askQuestionTo)})
		return
	}

	asksMu.Lock()
	defer asksMu.Unlock()
	t := d.loadThread(in.Repo, in.PR)
	msg := threadMessage{Role: "user", Text: in.Question, At: time.Now().UTC().Format(time.RFC3339)}
	t.Messages = append(t.Messages, msg)
	if err := d.writeThread(t); err != nil {
		writeJSONStatus(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, err := d.startRun("ask-pr", map[string]string{"repo": in.Repo, "pr": in.PR, "question": in.Question})
	if err != nil {
		// The question never reached awf: keep the thread as it was.
		t.Messages = t.Messages[:len(t.Messages)-1]
		d.writeThread(t)
		writeJSONStatus(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"execution_id": id, "message": msg})
}

func (d *dash) askAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, 405, map[string]string{"error": "POST only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, 403, map[string]string{"error": "loopback only"})
		return
	}
	var in struct {
		Repo string `json:"repo"`
		PR   string `json:"pr"`
		Run  string `json:"run"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "unreadable body"})
		return
	}
	if !repoNameRE.MatchString(in.Repo) || !prNumberRE.MatchString(in.PR) || !logID(in.Run) {
		writeJSONStatus(w, 422, map[string]string{"error": "repo, pr and run are required"})
		return
	}

	asksMu.Lock()
	defer asksMu.Unlock()
	dir := d.askDir(in.Repo, in.PR)
	t := d.loadThread(in.Repo, in.PR)
	for _, m := range t.Messages {
		if m.RunID == in.Run && m.Role == "assistant" {
			writeJSON(w, m)
			return
		}
	}
	text := strings.TrimSpace(readFile(filepath.Join(dir, "answer.md"), 1<<20))
	if text == "" {
		writeJSONStatus(w, 404, map[string]string{"error": "this run wrote no answer.md"})
		return
	}
	shown, suggest := parseSuggest(text)
	msg := threadMessage{Role: "assistant", Text: shown, At: time.Now().UTC().Format(time.RFC3339), RunID: in.Run, Cost: d.runCostSince(t, in.Run), Suggest: suggest}
	t.Messages = append(t.Messages, msg)
	if err := d.writeThread(t); err != nil {
		writeJSONStatus(w, 500, map[string]string{"error": err.Error()})
		return
	}
	// The next turn starts from a clean file, so a stale answer is never read
	// twice.
	os.Remove(filepath.Join(dir, "answer.md"))
	writeJSON(w, msg)
}
