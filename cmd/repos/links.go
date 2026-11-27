package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"opencode-dots/internal/store"
)

// dbLink holds the flags of `repos db link` (ADR 0041, first step). A workflow writes a
// proposal as a JSON file and a shell step inserts it with -file; the flags are the same
// fields for a person at the keyboard.
type dbLink struct {
	File, A, B, Reason, Evidence, ID, By string
	Confidence                           float64
	ConfidenceSet                        bool
}

// maxLinkFile caps a proposal file: it is a few lines, so a large file is a mistake.
const maxLinkFile = 64 << 10

// linkFile is the JSON a workflow writes. Unknown keys are refused, so a typo is not
// silently dropped.
type linkFile struct {
	A          string          `json:"a"`
	B          string          `json:"b"`
	Confidence *float64        `json:"confidence"`
	Reason     string          `json:"reason"`
	Evidence   json.RawMessage `json:"evidence"`
	ProposedBy string          `json:"proposed_by"`
}

// linkCommand runs `repos db link propose|list|accept|decline`. The text it stores is
// cleaned by the store: one paragraph, capped, no control characters. It is data.
func linkCommand(ctx context.Context, st *store.Store, out io.Writer, action string, l dbLink) error {
	switch action {
	case "propose":
		in := store.LinkInput{A: l.A, B: l.B, Reason: l.Reason, ProposedBy: l.By}
		if l.ConfidenceSet {
			in.Confidence = l.Confidence
		}
		if l.Evidence != "" {
			in.Evidence = json.RawMessage(l.Evidence)
		}
		if l.File != "" {
			f, err := readLinkFile(l.File)
			if err != nil {
				return err
			}
			in.A, in.B, in.Reason, in.Evidence = f.A, f.B, f.Reason, f.Evidence
			if f.Confidence == nil {
				return errors.New("repos db link propose: the file has no confidence")
			}
			in.Confidence = *f.Confidence
			if f.ProposedBy != "" {
				in.ProposedBy = f.ProposedBy
			}
		} else if !l.ConfidenceSet {
			return errors.New("repos db link propose: -confidence is required (or -file)")
		}
		if in.ProposedBy == "" {
			in.ProposedBy = "dot"
		}
		p, err := st.ProposeLink(ctx, in)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "link=%s %s <- %s confidence=%.2f open\n", p.ID, p.A, p.B, p.Confidence)
		return nil
	case "list":
		list, err := st.ListLinkProposals(ctx, 0)
		if err != nil {
			return err
		}
		for _, p := range list {
			fmt.Fprintf(out, "%s\t%s\t%.2f\t%s <- %s\t%s\t%s\n", p.ID, p.State, p.Confidence,
				p.A, p.B, p.CreatedAt.UTC().Format(time.RFC3339), p.Reason)
		}
		return nil
	case "accept", "decline":
		if l.ID == "" {
			return fmt.Errorf("repos db link %s: -id is required", action)
		}
		by := l.By
		if by == "" {
			by = "cli"
		}
		var p store.LinkProposal
		var err error
		if action == "accept" {
			p, err = st.AcceptLink(ctx, l.ID, by)
		} else {
			p, err = st.DeclineLink(ctx, l.ID, by)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "link=%s %s\n", p.ID, p.State)
		return nil
	}
	return fmt.Errorf("repos db link: unknown action %q (propose, list, accept, decline)", action)
}

func readLinkFile(path string) (linkFile, error) {
	fh, err := os.Open(path)
	if err != nil {
		return linkFile{}, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, maxLinkFile+1))
	if err != nil {
		return linkFile{}, err
	}
	if len(data) > maxLinkFile {
		return linkFile{}, fmt.Errorf("repos db link propose: %s is larger than %d bytes", path, maxLinkFile)
	}
	var f linkFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return linkFile{}, fmt.Errorf("repos db link propose: %s: %s", path, strings.TrimPrefix(err.Error(), "json: "))
	}
	return f, nil
}
