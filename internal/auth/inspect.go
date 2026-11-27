package auth

import "fmt"

// Read-only views of the state, for the dashboard's tests and its secret scans.

// SessionCount is how many sessions are held.
func (a *Auth) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// PendingCount is how many device flows are in progress.
func (a *Auth) PendingCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

// Lookup returns the session of a cookie id, expired or not.
func (a *Auth) Lookup(id string) (Session, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, s, ok := a.findSession(id)
	return s, ok
}

// ResetLimits forgets the rate-limit windows.
func (a *Auth) ResetLimits() {
	a.mu.Lock()
	a.limits = map[string]*window{}
	a.mu.Unlock()
}

// Dump prints everything held in memory, so a test can prove no secret is in it.
func (a *Auth) Dump() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fmt.Sprintf("%+v %#v %+v %+v %+v", a.sessions, a.sessions, a.pending, a.limits, a.readSeen)
}
