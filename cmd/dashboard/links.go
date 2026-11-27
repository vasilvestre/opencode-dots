package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencode-dots/internal/store"
)

// Link proposals (ADR 0041, first step, docs/links.md). The page lists them; Accept and
// Decline call one store operation each. The dashboard holds no GitHub token and writes
// nothing to a tracker: accepting moves references inside the database only.

// linkIDRE is the shape of a proposal id: lp_ and 128 random bits in hex.
var linkIDRE = regexp.MustCompile(`^lp_[0-9a-f]{32}$`)

type linkItemView struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	State      string   `json:"state"`
	Repo       string   `json:"repo"`
	Workspace  string   `json:"workspace"`
	Trust      string   `json:"trust"`
	Refs       []string `json:"refs"`
	MergedInto string   `json:"merged_into,omitempty"`
}

type linkView struct {
	ID         string          `json:"id"`
	State      string          `json:"state"`
	Confidence float64         `json:"confidence"`
	Reason     string          `json:"reason"`
	Evidence   json.RawMessage `json:"evidence"`
	ProposedBy string          `json:"proposed_by"`
	CreatedAt  string          `json:"created_at"`
	DecidedBy  string          `json:"decided_by,omitempty"`
	DecidedAt  string          `json:"decided_at,omitempty"`
	A          linkItemView    `json:"a"`
	B          linkItemView    `json:"b"`
	// Boundary is set when accepting would be refused: the two items differ in workspace
	// or trust level. The page disables Accept; the store refuses it anyway.
	Boundary bool `json:"boundary"`
}

func toLinkItem(it store.LinkItem) linkItemView {
	refs := it.Refs
	if refs == nil {
		refs = []string{}
	}
	return linkItemView{ID: it.ID, Title: it.Title, State: it.State, Repo: it.Repo, Workspace: it.Workspace,
		Trust: it.Trust, Refs: refs, MergedInto: it.MergedInto}
}

func toLinkView(p store.LinkProposal) linkView {
	v := linkView{ID: p.ID, State: p.State, Confidence: p.Confidence, Reason: p.Reason, Evidence: p.Evidence,
		ProposedBy: p.ProposedBy, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), DecidedBy: p.DecidedBy,
		A: toLinkItem(p.ItemA), B: toLinkItem(p.ItemB),
		Boundary: p.ItemA.Workspace != p.ItemB.Workspace || p.ItemA.Trust != p.ItemB.Trust}
	if !p.DecidedAt.IsZero() {
		v.DecidedAt = p.DecidedAt.UTC().Format(time.RFC3339)
	}
	if len(v.Evidence) == 0 {
		v.Evidence = json.RawMessage(`{}`)
	}
	return v
}

// links lists the proposals, open and decided, newest first: GET /api/links. It is read
// only and answers an empty list when there is no database.
func (d *dash) links(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	out := []linkView{}
	path := filepath.Join(d.state, "dots.db")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, out)
		return
	}
	st, err := store.Open(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot open the database"})
		return
	}
	defer st.Close()
	list, err := st.ListLinkProposals(r.Context(), 0)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot read the link proposals"})
		return
	}
	for _, p := range list {
		out = append(out, toLinkView(p))
	}
	writeJSON(w, out)
}

func (d *dash) linksAccept(w http.ResponseWriter, r *http.Request)  { d.linksDecide(w, r, true) }
func (d *dash) linksDecline(w http.ResponseWriter, r *http.Request) { d.linksDecide(w, r, false) }

// linksDecide accepts or declines one proposal: POST with {"id": "lp_..."}. The decider is
// the caller the audit log records. A refusal of the store (a boundary, a decided
// proposal) is a 409 with its message; the proposal stays as it was.
func (d *dash) linksDecide(w http.ResponseWriter, r *http.Request, accept bool) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "loopback only"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "JSON only"})
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !linkIDRE.MatchString(in.ID) {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "id must be a link proposal id"})
		return
	}
	auditTarget(r, in.ID)
	path := filepath.Join(d.state, "dots.db")
	if _, err := os.Stat(path); err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no such link proposal"})
		return
	}
	st, err := store.Open(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot open the database"})
		return
	}
	defer st.Close()
	by := d.auditCaller(r)
	var p store.LinkProposal
	if accept {
		p, err = st.AcceptLink(r.Context(), in.ID, by)
	} else {
		p, err = st.DeclineLink(r.Context(), in.ID, by)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no such link proposal"})
	case errors.Is(err, store.ErrLinkBoundary), errors.Is(err, store.ErrLinkClosed):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot decide the link proposal"})
	default:
		writeJSON(w, map[string]string{"id": p.ID, "state": p.State})
	}
}
