package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"opencode-dots/internal/spend"
)

// spendCommand prints what the runs of one UTC day cost (ADR 0036). With a
// repository it prints one number in USD, zero when the repository has no priced
// run; without one it prints a `repository<TAB>usd` line per repository, the most
// expensive first. The day is YYYY-MM-DD, default today. It reads the run logs the
// dashboard reads and nothing else; it never prints a prompt or a token.
func spendCommand(out io.Writer, dir, state, day, repo string) error {
	if state == "" {
		state = os.Getenv("STATE_DIR")
	}
	if state == "" {
		state = filepath.Join(dir, "state")
	}
	when := time.Now().UTC()
	if day != "" {
		t, err := time.Parse("2006-01-02", day)
		if err != nil {
			return fmt.Errorf("repos spend: -day must be YYYY-MM-DD, got %q", day)
		}
		when = t
	}
	per, skipped, err := spend.Day(state, spend.TranscriptDirs(state), when)
	if err != nil {
		return fmt.Errorf("repos spend: %w", err)
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "repos spend: %d index lines have no readable start time and are not counted\n", skipped)
	}
	if repo != "" {
		fmt.Fprintf(out, "%.4f\n", per[repo])
		return nil
	}
	names := make([]string, 0, len(per))
	for name := range per {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if per[names[i]] != per[names[j]] {
			return per[names[i]] > per[names[j]]
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		label := name
		if label == "" {
			label = "(no repository)"
		}
		fmt.Fprintf(out, "%s\t%.4f\n", label, per[name])
	}
	return nil
}
