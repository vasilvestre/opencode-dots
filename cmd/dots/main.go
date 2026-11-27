// Command dots reads the record of the dot's runs, for the person or the agent
// who debugs one. It only reads state/logs and awf's transcripts.
//
//	dots runs [--failed] [--workflow W] [--limit N] [--json]
//	dots show <id|prefix|last> [--json]
//	dots logs <id|prefix|last> [--step S] [--kind K] [--tail N] [--json]
//
// Exit codes: 0 ok, 1 the run shown failed, 2 usage or read error.
package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"opencode-dots/internal/runlog"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: dots runs|show|logs [flags]")
		return 2
	}
	root, _ := os.Getwd()
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errw)
	state := fs.String("state", cmp.Or(os.Getenv("STATE_DIR"), filepath.Join(root, "state")), "state directory")
	asJSON := fs.Bool("json", false, "print JSON")
	workflow := fs.String("workflow", "", "runs: only this workflow")
	failed := fs.Bool("failed", false, "runs: only failed runs")
	limit := fs.Int("limit", 20, "runs: how many, newest first (0 for all)")
	step := fs.String("step", "", "logs: only this step")
	kind := fs.String("kind", "", "logs: only events of this kind prefix, e.g. step.failed")
	tail := fs.Int("tail", 0, "logs: only the last N events")

	// Flags may follow the run reference: dots show last --json.
	var rest []string
	flagArgs := args[1:]
	for len(flagArgs) > 0 {
		if err := fs.Parse(flagArgs); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		rest = append(rest, fs.Arg(0))
		flagArgs = fs.Args()[1:]
	}
	store := runlog.New(*state)

	switch args[0] {
	case "runs":
		runs := store.List(runlog.Filter{Workflow: *workflow, Failed: *failed, Limit: *limit})
		if *asJSON {
			return printJSON(out, errw, runs)
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tWORKFLOW\tSTARTED\tSTATUS\tFAILED STEP\tCOST")
		for _, r := range runs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", short(r.ID), r.Workflow, clock(r.Started), r.Status, r.Failed, cost(r.Cost))
		}
		tw.Flush()
		return 0
	case "show", "logs":
		if len(rest) != 1 {
			fmt.Fprintf(errw, "usage: dots %s <id|prefix|last>\n", args[0])
			return 2
		}
		r, events, err := store.Get(rest[0])
		if err != nil {
			fmt.Fprintln(errw, "dots:", err)
			return 2
		}
		if args[0] == "logs" {
			return logs(out, errw, events, *step, *kind, *tail, *asJSON)
		}
		if *asJSON {
			if code := printJSON(out, errw, r); code != 0 {
				return code
			}
		} else {
			show(out, r, events)
		}
		if r.Status == "failed" {
			return 1
		}
		return 0
	}
	fmt.Fprintf(errw, "dots: unknown command %q\n", args[0])
	return 2
}

// show prints the run and, when it failed, the cause: the first failed step,
// its error, and the events that led to it. This is the page an agent reads
// first.
func show(w io.Writer, r runlog.Run, events []runlog.Event) {
	fmt.Fprintf(w, "run %s  %s  %s  %s\n", r.ID, r.Workflow, r.Started, r.Status)
	if r.Error != "" {
		fmt.Fprintf(w, "error: %s\n", r.Error)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, s := range r.Steps {
		dur := ""
		if s.MS > 0 {
			dur = fmt.Sprintf("%.1fs", float64(s.MS)/1000)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.Name, s.Status, dur, cost(s.Cost))
	}
	tw.Flush()
	if r.Failed == "" {
		return
	}
	fmt.Fprintf(w, "\nfirst failure: step %q\n", r.Failed)
	for _, s := range r.Steps {
		if s.Name == r.Failed && s.Status == "failed" {
			fmt.Fprintf(w, "  %s\n", s.Error)
		}
	}
	var before []runlog.Event
	for _, e := range events {
		if e.Step == r.Failed || e.Kind == "workflow.failed" {
			before = append(before, e)
		}
	}
	if len(before) > 6 {
		before = before[len(before)-6:]
	}
	fmt.Fprintln(w, "last events of that step:")
	for _, e := range before {
		fmt.Fprintf(w, "  %s\n", line(e, 300))
	}
	fmt.Fprintf(w, "\nfull log: dots logs %s --step %s\n", short(r.ID), r.Failed)
}

func logs(w, errw io.Writer, events []runlog.Event, step, kind string, tail int, asJSON bool) int {
	var keep []runlog.Event
	for _, e := range events {
		if step != "" && e.Step != step {
			continue
		}
		if kind != "" && !strings.HasPrefix(e.Kind, kind) {
			continue
		}
		keep = append(keep, e)
	}
	if tail > 0 && len(keep) > tail {
		keep = keep[len(keep)-tail:]
	}
	if asJSON {
		enc := json.NewEncoder(w)
		for _, e := range keep {
			if err := enc.Encode(e); err != nil {
				fmt.Fprintln(errw, "dots:", err)
				return 2
			}
		}
		return 0
	}
	for _, e := range keep {
		fmt.Fprintln(w, line(e, 0))
	}
	return 0
}

// line renders an event on one line, cutting the message text at max bytes
// (0 keeps it whole).
func line(e runlog.Event, max int) string {
	var b strings.Builder
	if e.At != "" {
		b.WriteString(clock(e.At) + " ")
	}
	b.WriteString(e.Kind)
	if e.Step != "" {
		b.WriteString(" " + e.Step)
	}
	if e.Error != "" {
		b.WriteString(" error: " + e.Error)
	}
	if t := strings.Join(strings.Fields(e.Text), " "); t != "" {
		if max > 0 && len(t) > max {
			t = t[:max] + "…"
		}
		b.WriteString(" | " + t)
	}
	return b.String()
}

func printJSON(w, errw io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(errw, "dots:", err)
		return 2
	}
	return 0
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func clock(ts string) string {
	if len(ts) >= 19 {
		return strings.Replace(ts[:19], "T", " ", 1)
	}
	return ts
}

func cost(c float64) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", c)
}
