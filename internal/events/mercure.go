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
	"strings"
	"time"
)

// Hub is a Mercure hub and the secret that signs the tokens it accepts.
type Hub struct {
	URL    string // the hub endpoint, such as https://hub.example/.well-known/mercure
	Secret []byte // the HS256 key shared with the hub
	Client *http.Client
}

// Event is one message received from the hub.
type Event struct {
	ID    string
	Type  string
	Topic string // not set by the hub in the stream: the topics subscribed to are in the request
	Data  string
}

// token signs a JWT with the Mercure claim for one role ("publish" or "subscribe"), for all
// topics. Narrower topic sets belong to the hub's own configuration.
func (h Hub) token(role string) string {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{"mercure": map[string]any{role: []string{"*"}}})
	body := header + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.Secret)
	mac.Write([]byte(body))
	return body + "." + enc.EncodeToString(mac.Sum(nil))
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
		q.Add("topic", t)
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
