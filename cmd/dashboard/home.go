package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// homeWindow is how far back the Home page counts runs and cost.
const homeWindow = 7 * 24 * time.Hour

// homeRepo is one row of the Home page: a repository with what the dot recorded for it.
// Every number belongs to this repository alone, so a row a viewer may not see can be
// dropped without leaving a trace in any other row.
type homeRepo struct {
	Repo      string  `json:"repo"`
	LastAt    string  `json:"lastAt,omitempty"`
	Runs7d    int     `json:"runs7d"`
	Cost7d    float64 `json:"cost7d"`
	Held      int     `json:"held"`
	Notes     int     `json:"notes"`
	HasMemory bool    `json:"hasMemory"`
}

// home lists every repository the dot knows with its latest activity, the runs and the
// cost of the last 7 days, the held records and the notes. It answers the owner's view;
// a visitor gets it through shapeHome, which keeps their repositories only.
func (d *dash) home(w http.ResponseWriter, r *http.Request) {
	rows := map[string]*homeRepo{}
	row := func(repo string) *homeRepo {
		if repo == "" {
			return nil
		}
		if x, ok := rows[repo]; ok {
			return x
		}
		x := &homeRepo{Repo: repo}
		rows[repo] = x
		return x
	}
	later := func(x *homeRepo, at time.Time) {
		if s := at.UTC().Format(time.RFC3339); s > x.LastAt {
			x.LastAt = s
		}
	}
	for _, name := range d.knownRepos() {
		row(name)
	}

	now := time.Now()
	if d.clock != nil {
		now = d.clock()
	}
	since := now.Add(-homeWindow)
	seen := map[string]bool{}
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) != nil || e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		x := row(d.targetOf(e.ID, d.runText(e.ID))["repo"])
		if x == nil {
			continue // a run with no known repository belongs to no row
		}
		at, err := time.Parse(time.RFC3339Nano, e.StartedAt)
		if err != nil {
			continue
		}
		later(x, at)
		if at.After(since) {
			usd, _ := d.runCost(e.ID)
			x.Runs7d++
			x.Cost7d += usd
		}
	}
	for _, j := range d.queueSpend() {
		x := row(j.Repo)
		if x == nil {
			continue
		}
		later(x, j.Created)
		if j.Created.After(since) {
			x.Runs7d++
			x.Cost7d += j.Cost
		}
	}
	for kind, items := range map[string][]gateItem{"held": d.gateItems("held"), "notes": d.gateItems("notes")} {
		for _, it := range items {
			x := row(repoOfRef(it.Ref))
			if x == nil {
				continue
			}
			if kind == "held" {
				x.Held++
			} else {
				x.Notes++
			}
			if at, err := time.Parse(time.RFC3339, it.At); err == nil {
				later(x, at)
			}
		}
	}
	for _, x := range rows {
		dir := filepath.Join(d.state, "tend", x.Repo)
		x.HasMemory = exists(filepath.Join(dir, "lessons.md")) || exists(filepath.Join(dir, "memory.md"))
	}
	writeJSON(w, sortedHome(rows))
}

func sortedHome(rows map[string]*homeRepo) []homeRepo {
	out := make([]homeRepo, 0, len(rows))
	for _, x := range rows {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAt != out[j].LastAt {
			return out[i].LastAt > out[j].LastAt
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// shapeHome keeps the rows of the repositories the visitor may see, and adds a row for a
// repository of their session the dot has recorded nothing about yet.
func shapeHome(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in []homeRepo
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	rows := map[string]*homeRepo{}
	for i := range in {
		if v.can(in[i].Repo) {
			rows[strings.ToLower(in[i].Repo)] = &in[i]
		}
	}
	for repo := range v.set {
		if _, ok := rows[repo]; !ok && repoNameRE.MatchString(repo) {
			rows[repo] = &homeRepo{Repo: repo}
		}
	}
	return encodeJSON(sortedHome(rows))
}
