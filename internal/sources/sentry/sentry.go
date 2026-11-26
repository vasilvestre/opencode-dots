// Package sentry is the adapter for Sentry issues (ADR 0039): the issue webhook
// (created, regression, resolved) and the list-issues API JSON.
//
// Trust: everything Sentry carries about an issue is attacker-influenced, because an
// application reports whatever its users make it say. The title, culprit, message, tags
// and stack frames are DATA. Nothing here interprets them: no field is parsed for a
// reference, a command, a path or a URL, and the reference is built only from the
// configured organization, the project slug and the issue id, each checked against the
// reference grammar. Strings are cut to a fixed length and stripped of control
// characters. A consumer must keep these fields out of the instruction part of any
// prompt and hand them over quoted, as untrusted input (ADR 0039).
package sentry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opencode-dots/internal/sources"
)

// Kinds this adapter emits.
const (
	KindCreated    = "sentry.issue.created"
	KindRegression = "sentry.issue.regression"
	KindResolved   = "sentry.issue.resolved"
	KindListed     = "sentry.issue.listed"
)

// Actor identifies the origin of the events. It is fixed: names in the payload are data.
const Actor = "sentry"

// MaxFrames is how many stack frames the payload keeps, innermost first.
const MaxFrames = 5

const maxText = 512

// Adapter parses Sentry JSON. Org is the organization slug: neither the webhook issue
// nor a list item carries it as a field, so the caller, which knows whose data it is
// reading, supplies it.
type Adapter struct {
	Org string
}

// Frame is one stack frame. All text is data.
type Frame struct {
	File     string `json:"file,omitempty"`
	Function string `json:"function,omitempty"`
	Line     int    `json:"line,omitempty"`
	InApp    bool   `json:"in_app"`
}

// Payload is the envelope payload. Every string is data from a third party.
type Payload struct {
	ID        string  `json:"id"`
	ShortID   string  `json:"short_id,omitempty"`
	Title     string  `json:"title"`
	Culprit   string  `json:"culprit,omitempty"`
	Level     string  `json:"level,omitempty"`
	Status    string  `json:"status,omitempty"`
	FirstSeen string  `json:"first_seen,omitempty"`
	LastSeen  string  `json:"last_seen,omitempty"`
	Count     int64   `json:"count"`
	Permalink string  `json:"permalink,omitempty"`
	Frames    []Frame `json:"frames,omitempty"`
}

type project struct {
	Slug string `json:"slug"`
}

type issue struct {
	ID        string          `json:"id"`
	ShortID   string          `json:"shortId"`
	Title     string          `json:"title"`
	Culprit   string          `json:"culprit"`
	Level     string          `json:"level"`
	Status    string          `json:"status"`
	FirstSeen string          `json:"firstSeen"`
	LastSeen  string          `json:"lastSeen"`
	Count     json.RawMessage `json:"count"` // Sentry sends a string
	Permalink string          `json:"permalink"`
	Project   project         `json:"project"`
}

type frame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Lineno   int    `json:"lineno"`
	InApp    bool   `json:"in_app"`
}

type exception struct {
	Values []struct {
		Stacktrace struct {
			Frames []frame `json:"frames"`
		} `json:"stacktrace"`
	} `json:"values"`
}

type hook struct {
	Action string `json:"action"`
	Data   struct {
		Issue issue `json:"issue"`
		// An error webhook carries the event; its frames are optional.
		Event struct {
			Exception exception `json:"exception"`
		} `json:"event"`
		Error struct {
			Exception exception `json:"exception"`
		} `json:"error"`
	} `json:"data"`
}

var _ sources.Adapter = Adapter{}

// Tracker implements sources.Adapter.
func (Adapter) Tracker() sources.Tracker { return sources.Sentry }

// Parse implements sources.Adapter. A JSON array is a list-issues response, an object a
// webhook body. In a list, an item that cannot be referenced is left out and reported in
// the error, the others are still returned. A webhook with an unknown action yields an
// error and no envelope.
func (a Adapter) Parse(raw []byte) ([]sources.Envelope, error) {
	s := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(s, "["):
		return a.parseList(raw)
	case strings.HasPrefix(s, "{"):
		return a.parseHook(raw)
	}
	return nil, errors.New("sentry: neither a webhook body nor a list of issues")
}

func (a Adapter) parseList(raw []byte) ([]sources.Envelope, error) {
	var items []issue
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("sentry: bad issue list: %w", err)
	}
	var out []sources.Envelope
	var errs []error
	for i, it := range items {
		// The key leaves out counts and times: a poll that sees the same issue again
		// yields the same key.
		e, err := a.envelope(it, KindListed, it.LastSeen, nil, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("sentry: list item %d: %w", i, err))
			continue
		}
		out = append(out, e)
	}
	return out, errors.Join(errs...)
}

func (a Adapter) parseHook(raw []byte) ([]sources.Envelope, error) {
	var h hook
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("sentry: bad webhook body: %w", err)
	}
	kind, when, dedupe := "", h.Data.Issue.LastSeen, h.Data.Issue.LastSeen
	switch h.Action {
	case "created":
		kind, when, dedupe = KindCreated, h.Data.Issue.FirstSeen, ""
	case "regression":
		kind = KindRegression
	case "resolved":
		kind = KindResolved
	default:
		return nil, fmt.Errorf("sentry: webhook action %q is not handled", clip(h.Action))
	}
	ex := h.Data.Event.Exception
	if len(ex.Values) == 0 {
		ex = h.Data.Error.Exception
	}
	e, err := a.envelope(h.Data.Issue, kind, when, frames(ex), dedupe)
	if err != nil {
		return nil, err
	}
	return []sources.Envelope{e}, nil
}

// envelope builds one envelope. dedupe is extra text for the key, for events that may
// happen again on the same issue (a regression, a resolve).
func (a Adapter) envelope(it issue, kind, when string, fr []Frame, dedupe string) (sources.Envelope, error) {
	ref, err := sources.NewRef(sources.Sentry, a.Org+"/"+it.Project.Slug+"#"+it.ID)
	if err != nil {
		return sources.Envelope{}, err
	}
	t, err := time.Parse(time.RFC3339, when)
	if err != nil {
		return sources.Envelope{}, fmt.Errorf("%s: time %q is not RFC 3339", ref, clip(when))
	}
	p := Payload{
		ID: it.ID, ShortID: clip(it.ShortID), Title: clip(it.Title), Culprit: clip(it.Culprit),
		Level: clip(it.Level), Status: clip(it.Status),
		FirstSeen: clip(it.FirstSeen), LastSeen: clip(it.LastSeen),
		Count: count(it.Count), Permalink: link(it.Permalink), Frames: fr,
	}
	body, err := json.Marshal(p)
	if err != nil {
		return sources.Envelope{}, err
	}
	parts := []string{ref.String(), kind}
	if dedupe != "" {
		parts = append(parts, dedupe)
	}
	return sources.Envelope{
		Ref: ref.String(), Kind: kind, Actor: Actor, Time: t.UTC(), Payload: body,
		Key: sources.IdempotencyKey(parts...),
	}, nil
}

// frames keeps the innermost frames: Sentry lists the oldest call first.
func frames(ex exception) []Frame {
	if len(ex.Values) == 0 {
		return nil
	}
	all := ex.Values[len(ex.Values)-1].Stacktrace.Frames
	var out []Frame
	for i := len(all) - 1; i >= 0 && len(out) < MaxFrames; i-- {
		f := all[i]
		out = append(out, Frame{File: clip(f.Filename), Function: clip(f.Function), Line: f.Lineno, InApp: f.InApp})
	}
	return out
}

// count reads Sentry's count, a string in the API and a number elsewhere. Anything else is 0.
func count(r json.RawMessage) int64 {
	var s string
	if json.Unmarshal(r, &s) != nil {
		s = strings.TrimSpace(string(r))
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// link keeps an https permalink and drops anything else. It is carried, never followed.
func link(s string) string {
	if !strings.HasPrefix(s, "https://") {
		return ""
	}
	return clip(s)
}

// clip strips control characters and cuts s to maxText runes.
func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxText {
		return string(r[:maxText])
	}
	return s
}
