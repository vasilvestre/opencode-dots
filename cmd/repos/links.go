package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opencode-dots/internal/atomicfile"
	"os"
	"path/filepath"
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
	// Workspace, Max and Out are for `candidates`: the workspace to look in, the cap on pairs,
	// and the directory that receives one JSON file per pair.
	Workspace, Out string
	Max            int
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

// linkCommand runs `repos db link propose|list|accept|decline|split|candidates`. The text it stores is
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
	case "candidates":
		return linkCandidates(ctx, st, out, l)
	case "accept", "decline", "split":
		if l.ID == "" {
			return fmt.Errorf("repos db link %s: the proposal id is required (-id, or as the argument)", action)
		}
		by := l.By
		if by == "" {
			by = "cli"
		}
		var p store.LinkProposal
		var err error
		switch action {
		case "accept":
			p, err = st.AcceptLink(ctx, l.ID, by)
		case "decline":
			p, err = st.DeclineLink(ctx, l.ID, by)
		default:
			p, err = st.SplitLink(ctx, l.ID, by)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "link=%s %s\n", p.ID, p.State)
		return nil
	}
	return fmt.Errorf("repos db link: unknown action %q (propose, list, accept, decline, split, candidates)", action)
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

// maxPayloadShown caps the payload a pair file carries: what a model reads is a summary, not
// the whole record. A larger payload is cut and marked.
const maxPayloadShown = 4000

type pairItem struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Repo             string          `json:"repo"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	PayloadTruncated string          `json:"payload_truncated,omitempty"`
}

type pairFile struct {
	A      pairItem `json:"a"`
	B      pairItem `json:"b"`
	Shared []string `json:"shared"`
}

func pairItemOf(it store.CandidateItem) pairItem {
	v := pairItem{ID: it.ID, Title: store.CleanLinkText(it.Title, 200), Repo: it.Repo}
	var buf bytes.Buffer
	if json.Compact(&buf, it.Payload) != nil {
		return v
	}
	if buf.Len() <= maxPayloadShown {
		v.Payload = json.RawMessage(buf.Bytes())
	} else {
		v.PayloadTruncated = store.CleanLinkText(buf.String(), maxPayloadShown)
	}
	return v
}

// linkCandidates lists the pairs a model may judge (store.LinkCandidates) and writes one
// pair-NNN.json per pair under -out, mode 0600. Each line printed is `pair-NNN a b tokens`.
func linkCandidates(ctx context.Context, st *store.Store, out io.Writer, l dbLink) error {
	if l.Out == "" {
		return errors.New("repos db link candidates: -out DIR is required")
	}
	list, refused, err := st.LinkCandidates(ctx, l.Workspace, l.Max)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Out, 0o700); err != nil {
		return err
	}
	for i, c := range list {
		name := fmt.Sprintf("pair-%03d", i+1)
		data, err := json.Marshal(pairFile{A: pairItemOf(c.A), B: pairItemOf(c.B), Shared: c.Shared})
		if err != nil {
			return err
		}
		if err := atomicfile.Write(filepath.Join(l.Out, name+".json"), data, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s %s %s\n", name, c.A.ID, c.B.ID, strings.Join(c.Shared, ","))
	}
	fmt.Fprintf(out, "candidates=%d refused_boundary=%d\n", len(list), refused)
	return nil
}
