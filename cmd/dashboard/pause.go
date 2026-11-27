package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The emergency stop (bin/pause, docs/pause.md): a file per switch under STATE_DIR/pause/, which the
// publish gate, tend and the queue read. The dashboard shows and flips the same files, owner only.

type pauseRecord struct {
	Who    string `json:"who"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

func (d *dash) pauseDir() string { return filepath.Join(d.state, "pause") }

func pauseSlug(repo string) string { return strings.ReplaceAll(repo, "/", "__") }

// isPaused is true when the whole dot or this repository is stopped.
func (d *dash) isPaused(repo string) bool {
	if _, err := os.Stat(filepath.Join(d.pauseDir(), "all.json")); err == nil {
		return true
	}
	if repo == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(d.pauseDir(), pauseSlug(repo)+".json"))
	return err == nil
}

func readPause(path string) *pauseRecord {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p pauseRecord
	if json.Unmarshal(raw, &p) != nil {
		p = pauseRecord{Reason: "unreadable record"} // a stop that cannot be read still stops
	}
	return &p
}

// pauseAPI: GET says what is stopped; POST {action: stop|resume, repo: "" for everything, reason}
// flips one switch.
func (d *dash) pauseAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out := map[string]any{"all": readPause(filepath.Join(d.pauseDir(), "all.json")), "repos": map[string]*pauseRecord{}}
		repos := out["repos"].(map[string]*pauseRecord)
		files, _ := filepath.Glob(filepath.Join(d.pauseDir(), "*__*.json"))
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".json")
			repos[strings.Replace(name, "__", "/", 1)] = readPause(f)
		}
		writeJSON(w, out)
	case http.MethodPost:
		var in struct {
			Action string `json:"action"`
			Repo   string `json:"repo"`
			Reason string `json:"reason"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in) != nil || (in.Action != "stop" && in.Action != "resume") {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "action is stop or resume"})
			return
		}
		name := "all"
		if in.Repo != "" {
			if !repoNameRE.MatchString(in.Repo) {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "repo is OWNER/REPO"})
				return
			}
			name = pauseSlug(in.Repo)
		}
		path := filepath.Join(d.pauseDir(), name+".json")
		if in.Action == "resume" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot raise the stop"})
				return
			}
		} else if err := writePause(d.pauseDir(), path, in.Reason); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot write the stop: a stop that is not written does not stop"})
			return
		}
		writeJSON(w, map[string]string{"status": map[string]string{"stop": "stopped", "resume": "resumed"}[in.Action]})
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func writePause(dir, path, reason string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil // already stopped: keep the first record
	}
	raw, _ := json.Marshal(pauseRecord{Who: "dashboard", Reason: strings.TrimSpace(reason), At: time.Now().UTC().Format(time.RFC3339)})
	return os.WriteFile(path, raw, 0o644)
}
