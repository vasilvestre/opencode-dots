package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"opencode-dots/internal/store"
)

// The audit log of ADR 0037: one row for every write the dashboard serves, with the endpoint,
// a target (a file name or a record id), the caller and the HTTP status. No request body, no
// secret, no role check: it records, it never refuses.
//
// A store that cannot take the row never blocks the endpoint: the write has already been
// answered, the failure is logged, and the next write tries again.

// tailscaleLoginHeader is what `tailscale serve` sets on a request it proxies.
const tailscaleLoginHeader = "Tailscale-User-Login"

// localCaller is the caller of a request that carries no trusted login: the owner at the
// machine, or a proxy that does not name the user.
const localCaller = "local"

type auditCtxKey struct{}

// auditNote is what a handler adds to the entry of its own request.
type auditNote struct{ target string }

// auditTarget names what the write touches, for the audit entry. A handler calls it once
// the name is validated; a request refused before that is recorded without a target.
func auditTarget(r *http.Request, target string) {
	if n, ok := r.Context().Value(auditCtxKey{}).(*auditNote); ok {
		n.target = target
	}
}

// statusWriter remembers the status code the handler answered.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// audited wraps a write endpoint: every request that is not a read gets one audit entry after
// the handler answered, whatever the answer was. A refused write is a row too. target is the
// fixed target of an endpoint that has one; a handler may set a finer one with auditTarget.
func (d *dash) audited(endpoint, target string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			h(w, r)
			return
		}
		note := &auditNote{target: target}
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := sw.status
			if status == 0 {
				status = http.StatusOK // net/http answers 200 when a handler writes nothing
			}
			if rec := recover(); rec != nil {
				d.recordAudit(r, endpoint, note.target, http.StatusInternalServerError)
				panic(rec)
			}
			d.recordAudit(r, endpoint, note.target, status)
		}()
		h(sw, r.WithContext(context.WithValue(r.Context(), auditCtxKey{}, note)))
	}
}

// auditCaller is who made the request. The tailnet login counts only on a request that passes
// the same trust check as a write (loopback or an -allow-host name, with a matching Origin):
// that is the request `tailscale serve` forwards. Anything else is "local".
func (d *dash) auditCaller(r *http.Request) string {
	if d.auth != nil && !ownerRequest(r) {
		if login := d.sessionLogin(r); login != "" {
			return login // a signed-in visitor is named by the GitHub login (ADR 0044)
		}
	}
	login := strings.TrimSpace(r.Header.Get(tailscaleLoginHeader))
	if login == "" || !d.trustedRequest(r) {
		if d.auth != nil && !ownerRequest(r) {
			return "anonymous"
		}
		return localCaller
	}
	return login
}

// recordAudit appends one entry. A failure is logged and goes no further.
func (d *dash) recordAudit(r *http.Request, endpoint, target string, status int) {
	d.appendAudit(r.Method+" "+endpoint, target, d.auditCaller(r), status)
}

// appendAudit writes one row for the endpoint ("METHOD /path"), whoever the caller is.
func (d *dash) appendAudit(endpoint, target, caller string, status int) {
	if d.state == "" {
		return
	}
	st, err := store.Open(filepath.Join(d.state, "dots.db"))
	if err != nil {
		log.Printf("audit: %s not recorded: %v", endpoint, err)
		return
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.RecordAudit(ctx, endpoint, target, caller, status); err != nil {
		log.Printf("audit: %s not recorded: %v", endpoint, err)
	}
}

// auditView is one entry for the Audit page.
type auditView struct {
	ID       int64  `json:"id"`
	At       string `json:"at"`
	Endpoint string `json:"endpoint"`
	Target   string `json:"target"`
	Caller   string `json:"caller"`
	Outcome  int    `json:"outcome"`
}

// audit lists the last writes, newest first: GET /api/audit?limit= (default 100, at most 500).
// It is read only and answers an empty list when there is no database yet.
func (d *dash) audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	out := []auditView{}
	path := filepath.Join(d.state, "dots.db")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, out)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	st, err := store.Open(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot open the database"})
		return
	}
	defer st.Close()
	entries, err := st.RecentAudit(r.Context(), limit)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot read the audit log"})
		return
	}
	for _, e := range entries {
		out = append(out, auditView{ID: e.ID, At: e.At.UTC().Format(time.RFC3339), Endpoint: e.Endpoint,
			Target: e.Target, Caller: e.Caller, Outcome: e.Outcome})
	}
	writeJSON(w, out)
}
