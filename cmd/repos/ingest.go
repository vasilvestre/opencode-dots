package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"opencode-dots/internal/store"
	"opencode-dots/internal/workitem"
)

// maxIngest caps what is read from stdin: a composer audit report is a few kilobytes.
const maxIngest = 16 << 20

// dbIngest holds the flags of `repos db ingest`.
type dbIngest struct {
	Source string // only "advisory"
	Repo   string
	Lock   string // optional composer.lock, for the installed versions
	Floor  string // optional classify-floor.txt (default: <dir>/workflows/data/classify-floor.txt)
	JSON   bool   // one JSON object per line instead of a tab separated line
}

// ingestCommand reads one source report on in and records it as work items of Repo
// (ADR 0039). It prints one line per advisory: status (new, seen, changed), reference,
// level, severity and package names, or the JSON of the result with -json.
func ingestCommand(ctx context.Context, st *store.Store, in io.Reader, out, errOut io.Writer, dir, workspace string, f dbIngest) error {
	if f.Source != "advisory" {
		return fmt.Errorf("repos db ingest: -source must be advisory, got %q", f.Source)
	}
	if f.Repo == "" {
		return errors.New("repos db ingest: -repo OWNER/REPO is required")
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxIngest+1))
	if err != nil {
		return err
	}
	if len(raw) > maxIngest {
		return fmt.Errorf("repos db ingest: input is larger than %d bytes", maxIngest)
	}
	var lock []byte
	if f.Lock != "" {
		if lock, err = os.ReadFile(f.Lock); err != nil {
			return err
		}
	}
	floorFile := f.Floor
	if floorFile == "" {
		floorFile = filepath.Join(dir, "workflows", "data", "classify-floor.txt")
	}
	// Like _classify-item: no floor file, no classification.
	floorData, err := os.ReadFile(floorFile)
	if err != nil {
		return fmt.Errorf("sensitive floor: %w (refusing to classify without it)", err)
	}
	results, leftOut, err := workitem.IngestAdvisories(ctx, st, workspace, f.Repo, raw, lock, workitem.ParseFloor(floorData))
	if leftOut != nil {
		fmt.Fprintln(errOut, "repos: left out:", oneLine(leftOut.Error()))
	}
	for _, r := range results {
		if f.JSON {
			b, merr := json.Marshal(r)
			if merr != nil {
				return merr
			}
			fmt.Fprintf(out, "%s\n", b)
			continue
		}
		names := make([]string, 0, len(r.Packages))
		for _, p := range r.Packages {
			names = append(names, p.Name)
		}
		fmt.Fprintf(out, "%s\t%s\tlevel=%d\tseverity=%s\t%s\n", r.Outcome, r.Ref, r.Level, r.Severity, strings.Join(names, ","))
	}
	if errors.Is(err, workitem.ErrUnknownProject) {
		return fmt.Errorf("%w (run `repos db import` first)", err)
	}
	return err
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
}
