// Package events is a small client of a Mercure hub (ADR 0026): it publishes an event and
// follows topics over server-sent events. The hub is a notification channel and never a
// store: a claim lives in the database, and a hub that is down loses a notification, not a
// claim. Mercure is a documented protocol, so no hub library is needed.
package events

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Hub is a Mercure hub and the secret that signs the tokens it accepts.
type Hub struct {
	URL    string // the hub endpoint, such as https://hub.example/.well-known/mercure
	Secret []byte // the HS256 key shared with the hub
	// Issuer is the `iss` the hub trusts (its MERCURE_TRUSTED_ISSUERS). Hub 1.1 refuses a token
	// without a trusted issuer and an audience (RFC 9068).
	Issuer string
	// Match and MatchType select the topics the tokens we sign may use. The default is every topic
	// ("*"); a hub shared with others should be given a narrower selector.
	Match, MatchType string
	Client           *http.Client
}

// detailType is the RFC 9396 authorization detail type hub 1.x reads in an access token.
const detailType = "https://mercure.rocks/authorization-detail"

// tokenLifetime is how long a token we sign stays valid: one request, with room for clock skew.
const tokenLifetime = 5 * time.Minute

// Event is one message received from the hub.
type Event struct {
	ID    string
	Type  string
	Topic string // not set by the hub in the stream: the topics subscribed to are in the request
	Data  string
}

// token signs a JWT with the Mercure claim for one role ("publish" or "subscribe"), for the
// topics the hub is configured with (every topic by default).
func (h Hub) token(role string) string {
	return h.sign(role, "dots", h.URL, []map[string]string{h.selector()}, tokenLifetime)
}

// SubscriberToken signs a token a person's client follows the hub with: the topics are exactly
// those named, nothing else. audience is the hub's own endpoint as the hub sees a request (a hub
// behind a TLS proxy sees http and the forwarded host); sub names who asked, for the hub's logs.
func (h Hub) SubscriberToken(sub, audience string, topics []string, ttl time.Duration) string {
	sel := make([]map[string]string, 0, len(topics))
	for _, t := range topics {
		sel = append(sel, map[string]string{"match": t, "match_type": "exact"})
	}
	return h.sign("subscribe", sub, audience, sel, ttl)
}

func (h Hub) sign(role, sub, audience string, topics []map[string]string, ttl time.Duration) string {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"at+jwt"}`)) // RFC 9068, which hub 1.1 requires
	now := time.Now()
	claims := map[string]any{
		// RFC 9396, the form hub 1.x reads: the pre-1.0 "mercure" claim is not accepted.
		"authorization_details": []map[string]any{{"type": detailType, "actions": []string{role}, "topics": topics}},
		"sub":                   sub,
		"jti":                   strconv.FormatInt(now.UnixNano(), 10),
		"client_id":             "dots",
		"aud":                   audience, // the hub's resource identifier: its own endpoint, without a query
		"iat":                   now.Unix(),
		"exp":                   now.Add(ttl).Unix(),
	}
	if h.Issuer != "" {
		claims["iss"] = h.Issuer
	}
	payload, _ := json.Marshal(claims)
	body := header + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.Secret)
	mac.Write([]byte(body))
	return body + "." + enc.EncodeToString(mac.Sum(nil))
}

func (h Hub) selector() map[string]string {
	match, typ := h.Match, h.MatchType
	if match == "" {
		match = "*" // every topic
	}
	if typ == "" {
		typ = "exact"
	}
	return map[string]string{"match": match, "match_type": typ}
}

func (h Hub) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Publish sends one update on a topic. A private update only reaches subscribers whose token
// allows the topic.
func (h Hub) Publish(ctx context.Context, topic string, data []byte, private bool) error {
	if h.URL == "" || len(h.Secret) == 0 {
		return errors.New("events: a hub needs a URL and a secret")
	}
	form := url.Values{"topic": {topic}, "data": {string(data)}}
	if private {
		form.Set("private", "on")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+h.token("publish"))
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("events: the hub answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Subscribe follows topics until the context ends and sends each update on the channel. A
// dropped connection is reopened after a pause with Last-Event-ID, so the hub replays what was
// missed. The channel is closed when the context ends.
func (h Hub) Subscribe(ctx context.Context, topics ...string) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		lastID := ""
		for ctx.Err() == nil {
			lastID = h.follow(ctx, topics, lastID, out)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return out
}

func (h Hub) follow(ctx context.Context, topics []string, lastID string, out chan<- Event) string {
	u, err := url.Parse(h.URL)
	if err != nil {
		return lastID
	}
	q := u.Query()
	for _, t := range topics {
		q.Add("match", t) // hub 1.x: `match` for an exact topic, `match_urlpattern` for a pattern
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return lastID
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+h.token("subscribe"))
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	// A stream has no overall timeout: only the context ends it.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return lastID
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return lastID
	}
	var ev Event
	var data []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 || ev.ID != "" {
				ev.Data = strings.Join(data, "\n")
				select {
				case out <- ev:
				case <-ctx.Done():
					return lastID
				}
				if ev.ID != "" {
					lastID = ev.ID
				}
			}
			ev, data = Event{}, nil
		case strings.HasPrefix(line, ":"):
			// a comment, used by hubs as a keep-alive
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "id":
				ev.ID = value
			case "event":
				ev.Type = value
			case "data":
				data = append(data, value)
			}
		}
	}
	return lastID
}

// ClaimUpdate is the payload published when a claim changes (ADR 0026).
type ClaimUpdate struct {
	Kind       string `json:"kind"` // created, renewed, released
	ClaimID    string `json:"claim"`
	WorkItemID string `json:"work_item"`
	ActorID    string `json:"actor"`
	Machine    string `json:"machine"`
	Branch     string `json:"branch,omitempty"`
}

// ClaimTopic is the topic of the claims of one work item.
func ClaimTopic(workItemID string) string { return "dots/claim/" + workItemID }

// RepoTopic is the topic every event about one repository goes to: jobs for a worker, claims,
// pickups. A subscriber token names these topics, so what a person may follow is the set of
// repositories they may reach. The name is lower-cased, as GitHub compares them.
func RepoTopic(repo string) string { return "dots/repo/" + strings.ToLower(repo) }
