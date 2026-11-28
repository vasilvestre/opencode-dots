package main

import (
	"net/http"
	"net/http/httputil"
	"path/filepath"
	"strings"
)

// routes wires every handler. A read endpoint goes through scoped (what a visitor may see), a
// change or a config page through ownerOnly; with sign-in off both are the handler itself.
func routes(d *dash, ui string, proxy *httputil.ReverseProxy) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Everything the page needs lives in ui/: modules, stylesheet, icon.
			if name := strings.TrimPrefix(r.URL.Path, "/"); safeAsset(name) {
				serveFile(w, r, filepath.Join(filepath.Dir(ui), name), assetType(name))
				return
			}
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, ui, "text/html; charset=utf-8")
	})
	repoField := rule{shape: byField("repo")}
	mux.HandleFunc("/auth/device/start", d.deviceStart)
	mux.HandleFunc("/auth/device/poll", d.devicePoll)
	mux.HandleFunc("/auth/logout", d.logout)
	mux.HandleFunc("/api/session", d.sessionInfo)
	mux.HandleFunc("/api/hub-token", d.hubToken)
	mux.HandleFunc("/queue/api/workflows/", d.ownerOnly(d.queueRun))
	mux.HandleFunc("/queue/api/executions/", d.ownerOnly(d.queueCancel))
	mux.HandleFunc("/api/queue", d.ownerOnly(d.queueList))
	mux.HandleFunc("/api/queue/workers", d.ownerOnly(d.queueWorkers))
	mux.HandleFunc("/api/checkouts", d.ownerOnly(d.checkouts))
	d.ownerWrite(mux, "/api/checkouts/clone", "", d.checkoutClone)
	d.ownerWrite(mux, "/api/pause", "", d.pauseAPI)
	mux.HandleFunc("/queue/api/executions", d.ownerOnly(d.queueExecutions))
	mux.HandleFunc("/queue/api/history", d.ownerOnly(d.queueHistory))
	d.ownerWrite(mux, "/api/worker-tokens", "", d.workerTokensAPI)
	d.ownerWrite(mux, "/api/worker-tokens/revoke", "", d.workerTokenRevoke)
	mux.HandleFunc("/api/artifacts", scoped(d, d.artifacts, rule{shape: shapeArtifacts}))
	mux.HandleFunc("/api/file", scoped(d, d.file, rule{gate: gateFile}))
	mux.HandleFunc("/api/log", scoped(d, d.viaExecutor(d.log), ruleRun))
	mux.HandleFunc("/api/stream", scoped(d, d.viaExecutor(d.stream), ruleRun))
	mux.HandleFunc("/api/cost", scoped(d, d.viaExecutor(d.cost), ruleRun))
	mux.HandleFunc("/api/home", scoped(d, d.home, rule{shape: shapeHome}))
	mux.HandleFunc("/api/usage", scoped(d, d.usage, rule{shape: shapeUsage}))
	mux.HandleFunc("/api/run", scoped(d, d.viaExecutor(d.runTimeline), ruleRun))
	mux.HandleFunc("/api/sessions", scoped(d, d.sessions, ruleRun))
	mux.HandleFunc("/api/logs", scoped(d, d.logs, repoField))
	mux.HandleFunc("/api/remote-runs", scoped(d, d.remoteRuns, rule{shape: shapeRemote}))
	mux.HandleFunc("/api/repos", scoped(d, d.repos, repoField))
	mux.HandleFunc("/api/repos/names", scoped(d, d.repoNames, rule{shape: shapeRepoNames}))
	d.ownerWrite(mux, "/api/repos/config", "", d.reposConfig)
	mux.HandleFunc("/api/replay", scoped(d, d.replay, ruleRun))
	mux.HandleFunc("/api/thread", scoped(d, d.thread, rule{gate: gateThread}))
	d.ownerWrite(mux, "/api/ask", "", d.ask)
	d.ownerWrite(mux, "/api/ask/answer", "", d.askAnswer)
	mux.HandleFunc("/api/memory", scoped(d, d.tendMemory, repoField))
	mux.HandleFunc("/api/files", d.ownerOnly(d.files))
	d.ownerWrite(mux, "/api/models", "model-presets", d.models)
	mux.HandleFunc("/api/claims", scoped(d, d.claims, repoField))
	mux.HandleFunc("/api/audit", scoped(d, d.audit, rule{shape: shapeAudit, prep: widenAudit}))
	mux.HandleFunc("/api/links", scoped(d, d.links, rule{shape: shapeLinks}))
	d.ownerWrite(mux, "/api/links/accept", "", d.linksAccept)
	d.ownerWrite(mux, "/api/links/decline", "", d.linksDecline)
	d.ownerWrite(mux, "/api/links/split", "", d.linksSplit)
	mux.HandleFunc("/api/workers", d.ownerOnly(d.workers))
	mux.HandleFunc("/api/autonomy", scoped(d, d.autonomy, rule{shape: shapeAutonomy}))
	mux.HandleFunc("/api/autonomy/artifact", scoped(d, d.autonomyArtifact, rule{gate: gateAutonomyArtifact}))
	d.ownerWrite(mux, "/api/autonomy/release", "", d.autonomyRelease)
	mux.HandleFunc("/api/health", d.ownerOnly(d.health))
	d.ownerWrite(mux, "/api/notifications", "notifications", d.notifications)
	mux.HandleFunc("/api/source", d.ownerOnly(d.source))
	d.ownerWrite(mux, "/api/save", "", d.saveFile)
	// A write through the proxy (starting or cancelling a run) is a write too: its target is the
	// awf path, and the audit never sees the body.
	awfProxy := http.StripPrefix("/awf", proxy)
	d.ownerWrite(mux, "/awf/", "", func(w http.ResponseWriter, r *http.Request) {
		auditTarget(r, r.URL.Path)
		awfProxy.ServeHTTP(w, r)
	})
	return mux
}
