package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"opencode-dots/internal/spend"
)

// cost reports what one run spent. Since awf v0.12.0 the provider price of an
// agent step travels with the run as `cost_usd`, so the number is read from the
// run's own log: no second database, no time window to guess.
func (d *dash) cost(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	usd, steps := d.runCost(id)
	writeJSON(w, runCost{Available: true, Cost: usd, Steps: steps})
}

type runCost struct {
	Available bool    `json:"available"`
	Cost      float64 `json:"cost"`
	Steps     int     `json:"steps"`
}

// usageOne is one pull request, or one repository when the run names no pull
// request, with the runs that touched it and what each workflow spent.
type usageOne struct {
	Key       string      `json:"key"`
	Repo      string      `json:"repo"`
	PR        string      `json:"pr,omitempty"`
	Issue     string      `json:"issue,omitempty"`
	Cost      float64     `json:"cost"`
	Runs      int         `json:"runs"`
	Unpriced  int         `json:"unpriced"`
	Workflows []usageFlow `json:"workflows"`
}

type usageFlow struct {
	Workflow string  `json:"workflow"`
	Cost     float64 `json:"cost"`
	Runs     int     `json:"runs"`
}

// usage adds up the price of every recorded run, grouped by pull request or
// issue. The grouping comes from the run log, like replay: the inputs are not
// stored anywhere else. Only runs with a log on disk are counted, so the totals
// cover what the recorder kept.
func (d *dash) usage(w http.ResponseWriter, r *http.Request) {
	type group struct {
		one  usageOne
		flow map[string]*usageFlow
	}
	groups := map[string]*group{}
	var total float64
	var runs, unpriced int

	add := func(repo, pr, issue, workflow string, usd float64) {
		if pr == "" && issue != "" {
			pr = "issue " + issue
		}
		key := repo
		if repo != "" && pr != "" {
			key = repo + "#" + pr
		}
		if key == "" {
			key = "(no pull request)"
		}
		g, ok := groups[key]
		if !ok {
			g = &group{one: usageOne{Key: key, Repo: repo, PR: pr, Issue: issue}, flow: map[string]*usageFlow{}}
			groups[key] = g
		}
		g.one.Runs++
		runs++
		g.one.Cost += usd
		total += usd
		if usd == 0 {
			g.one.Unpriced++
			unpriced++
		}
		f, ok := g.flow[workflow]
		if !ok {
			f = &usageFlow{Workflow: workflow}
			g.flow[workflow] = f
		}
		f.Runs++
		f.Cost += usd
	}
	for _, e := range d.rec.Entries() {
		in := d.targetOf(e.ID, d.runText(e.ID))
		usd, _ := d.runCost(e.ID)
		add(in["repo"], in["pr"], in["issue"], e.WorkflowName, usd)
	}
	// Runs a worker did on another machine have no log here: the worker reported their price.
	for _, j := range d.queueSpend() {
		in := map[string]string{}
		for _, k := range []string{"pr", "issue"} {
			in[k], _ = j.Inputs[k].(string)
		}
		add(j.Repo, in["pr"], in["issue"], j.Workflow, j.Cost)
	}

	out := []usageOne{}
	for _, g := range groups {
		for _, f := range g.flow {
			g.one.Workflows = append(g.one.Workflows, *f)
		}
		sort.Slice(g.one.Workflows, func(i, j int) bool { return g.one.Workflows[i].Cost > g.one.Workflows[j].Cost })
		out = append(out, g.one)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Key < out[j].Key
	})
	writeJSON(w, map[string]any{
		"total":    total,
		"runs":     runs,
		"unpriced": unpriced,
		"groups":   out,
	})
}

// costAnswer is what a run's log is worth: its price and how many agent steps carried one.
type costAnswer struct {
	usd   float64
	steps int
}

// runCost sums the prices recorded in a run's log. The transcript carries them;
// the recorder file is the fallback, and it carries them too once awf puts the
// field on the stream. A run started by an older awf has no price and reports
// zero, which the page shows as no price.
func (d *dash) runCost(id string) (float64, int) {
	path := d.transcriptPath(id)
	if path == "" {
		path = filepath.Join(d.state, "logs", id+".jsonl")
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	if memo := d.costs.get(id); memo.fresh(st) {
		return memo.val.usd, memo.val.steps
	}
	usd, steps := spend.SumCost(readFile(path, 8<<20))
	d.costs.put(id, st, costAnswer{usd, steps})
	return usd, steps
}

// askAnswer moves the answer.md a run wrote into the thread. It is idempotent:
// a run id lands in the thread once, so a retried request adds nothing.
// runCostSince prices one answer: awf records what each agent step cost, and
// the transcript of this run carries it. Zero means no price is known (a run
// from an awf without the field, or no agent step), never free.
func (d *dash) runCostSince(t thread, run string) float64 {
	if run == "" {
		return 0
	}
	usd, _ := d.runCost(run)
	return usd
}
