// Package sources is the inbound side of the work-source adapters (ADR 0023, ADR 0039).
//
// An adapter turns a raw event from one source into envelopes. Outbound writes (a
// comment, a transition) stay in shell steps (ADR 0002), so nothing here talks to a
// service. Text carried in an envelope is data: no code in this package interprets it,
// and a reference is only ever taken from a structured field.
package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Envelope is what every adapter produces, whatever its source.
type Envelope struct {
	// Ref names the work, as tracker:key. It is valid per ParseRef.
	Ref string `json:"ref"`
	// Kind says what happened, such as "advisory.reported". Adapters own their kinds.
	Kind string `json:"kind"`
	// Actor identifies who or what caused the event, never empty.
	Actor string `json:"actor"`
	// Time is when the source says the event happened.
	Time time.Time `json:"time"`
	// Payload is the source's own fields. Its text is data, not instruction.
	Payload json.RawMessage `json:"payload"`
	// Key is stable across polls: the same event always has the same key.
	Key string `json:"key"`
}

// Validate checks the fields every consumer relies on.
func (e Envelope) Validate() error {
	if _, err := ParseRef(e.Ref); err != nil {
		return err
	}
	switch {
	case e.Kind == "":
		return fmt.Errorf("sources: envelope %s has no kind", e.Ref)
	case e.Actor == "":
		return fmt.Errorf("sources: envelope %s has no actor", e.Ref)
	case e.Key == "":
		return fmt.Errorf("sources: envelope %s has no key", e.Ref)
	case !json.Valid(e.Payload):
		return fmt.Errorf("sources: envelope %s has an invalid payload", e.Ref)
	}
	return nil
}

// Adapter turns a raw event of one source into envelopes. It is inbound only.
// A partial result with an error is allowed: the envelopes returned are valid, and the
// error says what was left out.
type Adapter interface {
	// Tracker is the reference tracker the adapter feeds.
	Tracker() Tracker
	Parse(raw []byte) ([]Envelope, error)
}

// IdempotencyKey derives a stable key from the parts that make an event what it is,
// such as its reference, its kind and the source's own event id. Parts are length
// prefixed, so ("ab","c") and ("a","bc") differ. Never include a timestamp that changes
// between polls.
func IdempotencyKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p)) + ":"))
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
