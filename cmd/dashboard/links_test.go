package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

// linkState makes a state directory whose database holds two open work items and one
// proposal over them, plus a third item under another trust level.
func linkState(t *testing.T) (state string, prop, boundary store.LinkProposal) {
	t.Helper()
	state = t.TempDir()
	st, err := store.Open(filepath.Join(state, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	a, _ := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: "Ada"})
	p, _, _ := st.UpsertProject(ctx, "default", "acme/app", 0, nil)
	c, _, _ := st.UpsertProject(ctx, "default", "acme/client", 1, map[string]any{"trust": "client"})
	w1, _ := st.CreateWorkItem(ctx, p.ID, "one", a.ID, "github:acme/app#1")
	w2, _ := st.CreateWorkItem(ctx, p.ID, "two", a.ID, "jira:ABC-2")
	w3, _ := st.CreateWorkItem(ctx, c.ID, "three", a.ID, "sentry:s-3")
	prop, err = st.ProposeLink(ctx, store.LinkInput{A: w1.ID, B: w2.ID, Confidence: 0.8, Reason: "same trace", ProposedBy: "tend-repo"})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err = st.ProposeLink(ctx, store.LinkInput{A: w1.ID, B: w3.ID, Confidence: 0.6, ProposedBy: "tend-repo"})
	if err != nil {
		t.Fatal(err)
	}
	return state, prop, boundary
}

func linkPost(h http.HandlerFunc, path, host, ctype, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Host = host
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestLinksListsOpenAndDecidedNewestFirst(t *testing.T) {
	state, prop, boundary := linkState(t)
	d := &dash{state: state, allow: map[string]bool{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/links", nil)
	r.Host = "127.0.0.1:8099"
	d.links(w, r)
	var got []linkView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("body = %s, %v", w.Body, err)
	}
	if got[0].ID != boundary.ID || !got[0].Boundary || got[1].ID != prop.ID || got[1].Boundary {
		t.Errorf("list = %+v", got)
	}
	if got[1].A.Repo != "acme/app" || len(got[1].B.Refs) != 1 || got[1].Reason != "same trace" {
		t.Errorf("view = %+v", got[1])
	}

	// A foreign host and another method are refused; no database is an empty list.
	r = httptest.NewRequest(http.MethodGet, "/api/links", nil)
	r.Host = "evil.example"
	w = httptest.NewRecorder()
	d.links(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("foreign host: %d", w.Code)
	}
	empty := &dash{state: t.TempDir(), allow: map[string]bool{}}
	r = httptest.NewRequest(http.MethodGet, "/api/links", nil)
	r.Host = "localhost:8099"
	w = httptest.NewRecorder()
	empty.links(w, r)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("no db: %s", w.Body)
	}
}

func TestLinksAcceptDeclineValidateAndAreAudited(t *testing.T) {
	state, prop, boundary := linkState(t)
	d := &dash{state: state, allow: map[string]bool{}}
	accept := d.audited("/api/links/accept", "", d.linksAccept)
	decline := d.audited("/api/links/decline", "", d.linksDecline)
	const host = "127.0.0.1:8099"

	for name, c := range map[string]struct {
		h           http.HandlerFunc
		host, ctype string
		body        string
		want        int
	}{
		"foreign host":     {accept, "evil.example", "application/json", `{"id":"` + prop.ID + `"}`, 403},
		"not json":         {accept, host, "text/plain", `{"id":"` + prop.ID + `"}`, 415},
		"bad id":           {accept, host, "application/json", `{"id":"../x"}`, 422},
		"no body":          {accept, host, "application/json", ``, 422},
		"unknown proposal": {accept, host, "application/json", `{"id":"lp_` + strings.Repeat("0", 32) + `"}`, 404},
		"boundary":         {accept, host, "application/json", `{"id":"` + boundary.ID + `"}`, 409},
	} {
		if w := linkPost(c.h, "/api/links/accept", c.host, c.ctype, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := linkGet(d, "GET"); w != http.StatusMethodNotAllowed {
		t.Errorf("GET accept: %d", w)
	}

	// Still open after every refusal.
	st, _ := store.Open(filepath.Join(state, "dots.db"))
	defer st.Close()
	for _, p := range []store.LinkProposal{prop, boundary} {
		if got, _ := st.LinkProposalByID(context.Background(), p.ID); got.State != "open" {
			t.Fatalf("%s became %s after a refusal", p.ID, got.State)
		}
	}

	if w := linkPost(accept, "/api/links/accept", host, "application/json; charset=utf-8", `{"id":"`+prop.ID+`"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted"`) {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := linkPost(accept, "/api/links/accept", host, "application/json", `{"id":"`+prop.ID+`"}`); w.Code != 409 {
		t.Errorf("accept again: %d", w.Code)
	}
	if w := linkPost(decline, "/api/links/decline", host, "application/json", `{"id":"`+boundary.ID+`"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"declined"`) {
		t.Errorf("decline: %d %s", w.Code, w.Body)
	}
	got, _ := st.LinkProposalByID(context.Background(), prop.ID)
	if got.DecidedBy != "local" || got.ItemB.State != "merged" || len(got.ItemA.Refs) != 2 {
		t.Errorf("after accept = %+v", got)
	}

	// Every write left an audit row, refused ones included, with the proposal id as target
	// once the id was validated.
	rows := auditRows(t, state)
	if len(rows) != 9 {
		t.Fatalf("%d audit rows: %+v", len(rows), rows)
	}
	var targets, accepted int
	for _, r := range rows {
		if strings.HasPrefix(r.Endpoint, "POST /api/links/") {
			if r.Target == prop.ID || r.Target == boundary.ID {
				targets++
			}
			if r.Outcome == 200 {
				accepted++
			}
		}
	}
	if targets < 4 || accepted != 2 {
		t.Errorf("targets %d, ok %d, rows %+v", targets, accepted, rows)
	}
}

func linkGet(d *dash, _ string) int {
	r := httptest.NewRequest(http.MethodGet, "/api/links/accept", nil)
	r.Host = "127.0.0.1:8099"
	w := httptest.NewRecorder()
	d.audited("/api/links/accept", "", d.linksAccept)(w, r)
	return w.Code
}

func TestLinksSplitValidatesAuditsAndUndoesAnAccept(t *testing.T) {
	state, prop, boundary := linkState(t)
	d := &dash{state: state, allow: map[string]bool{}}
	accept := d.audited("/api/links/accept", "", d.linksAccept)
	split := d.audited("/api/links/split", "", d.linksSplit)
	const host = "127.0.0.1:8099"
	post := func(h http.HandlerFunc, host, ctype, body string) int {
		return linkPost(h, "/api/links/split", host, ctype, body).Code
	}
	for name, c := range map[string]struct {
		host, ctype, body string
		want              int
	}{
		"foreign host":     {"evil.example", "application/json", `{"id":"` + prop.ID + `"}`, 403},
		"not json":         {host, "text/plain", `{"id":"` + prop.ID + `"}`, 415},
		"bad id":           {host, "application/json", `{"id":"../x"}`, 422},
		"no body":          {host, "application/json", ``, 422},
		"unknown proposal": {host, "application/json", `{"id":"lp_` + strings.Repeat("0", 32) + `"}`, 404},
		"still open":       {host, "application/json", `{"id":"` + prop.ID + `"}`, 409},
	} {
		if got := post(split, c.host, c.ctype, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/links/split", nil)
	r.Host = host
	w := httptest.NewRecorder()
	split(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET split: %d", w.Code)
	}

	if w := linkPost(accept, "/api/links/accept", host, "application/json", `{"id":"`+prop.ID+`"}`); w.Code != 200 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := linkPost(split, "/api/links/split", host, "application/json", `{"id":"`+prop.ID+`"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"split"`) {
		t.Fatalf("split: %d %s", w.Code, w.Body)
	}
	if got := post(split, host, "application/json", `{"id":"`+prop.ID+`"}`); got != 409 {
		t.Errorf("split again: %d", got)
	}
	// A proposal that was never accepted cannot be split.
	if got := post(split, host, "application/json", `{"id":"`+boundary.ID+`"}`); got != 409 {
		t.Errorf("split of an open boundary proposal: %d", got)
	}

	st, _ := store.Open(filepath.Join(state, "dots.db"))
	defer st.Close()
	got, _ := st.LinkProposalByID(context.Background(), prop.ID)
	if got.State != "split" || got.SplitBy != "local" || got.ItemB.State != "open" || len(got.ItemB.Refs) != 1 || len(got.ItemA.Refs) != 1 {
		t.Errorf("after split = %+v", got)
	}
	var splits, ok int
	for _, r := range auditRows(t, state) {
		if r.Endpoint == "POST /api/links/split" {
			splits++
			if r.Outcome == 200 {
				ok++
			}
			if r.Target != "" && r.Target != prop.ID && r.Target != boundary.ID && !strings.HasPrefix(r.Target, "lp_") {
				t.Errorf("target = %q", r.Target)
			}
		}
	}
	if splits < 9 || ok != 1 {
		t.Errorf("audit rows: %d split calls, %d ok", splits, ok)
	}
}
