import { h, mount } from "../dom.js";
import { awf, getJSON, getText } from "../api.js";
import { ago, dur, elapsed, eventLine, formatLog, when } from "../format.js";
import { toast } from "../toast.js";
import { confirmDialog, openModal } from "../modal.js";
import { openRunModal, recalledRun } from "../run.js";

const REFRESH_MS = 5000;
const ARTIFACTS_MS = 15000;
const MAX_LOG_LINES = 800;

export function mountActivity(el) {
  const state = {
    executions: [],
    history: [],
    stats: { total: 0, failed: 0, avg: 0 },
    runs: [],
    filter: "",
    status: "all",
    artifacts: { reviews: [], replies: [], seen: [] },
    sessions: [],
    costs: new Map(),
  };
  const fileCache = new Map();
  const runCards = new Map(); // execution id -> {card, step, time, stream}
  const logText = new Map();

  // --- skeleton -----------------------------------------------------------
  const kpis = h("div", { class: "kpis", "aria-label": "Summary" });
  const runs = h("div", {});
  const runsCount = h("span", { class: "muted" }, "");
  const historyBody = h("tbody", {});
  const historyCount = h("span", { class: "muted" }, "");
  const reviews = h("div", { class: "list" });
  const replies = h("div", { class: "list" });
  const seen = h("div", {});

  const search = h("input", {
    type: "search",
    placeholder: "Filter by workflow",
    "aria-label": "Filter history by workflow",
    oninput: () => { state.filter = search.value.trim().toLowerCase(); renderHistory(); },
  });
  const status = h("select", {
    "aria-label": "Filter history by status",
    onchange: () => { state.status = status.value; renderHistory(); },
  },
  ["all", "succeeded", "failed", "running"].map((s) => h("option", { value: s }, s === "all" ? "All statuses" : s)),
  );

  mount(el,
    h("div", { class: "stack" },
      kpis,
      h("section", {},
        h("div", { class: "section-head" }, h("h2", {}, "Running now"), runsCount),
        runs,
      ),
      h("section", {},
        h("div", { class: "section-head" },
          h("h2", {}, "History"),
          historyCount,
          h("span", { class: "spacer" }),
          search,
          status,
          h("button", { class: "btn sm", onclick: () => refresh() }, "Refresh"),
        ),
        h("div", { class: "table-wrap" },
          h("table", {},
            h("thead", {}, h("tr", {},
              h("th", {}, "Workflow"), h("th", {}, "Status"), h("th", {}, "Started"),
              h("th", {}, "Duration"), h("th", {}, "Cost"), h("th", {}, "Run id"),
              h("th", { class: "actions" }, "Log"),
            )),
            historyBody,
          ),
        ),
      ),
      h("section", {},
        h("div", { class: "section-head" }, h("h2", {}, "Pull requests"), h("span", { class: "muted" }, "reviews and drafted replies")),
        h("div", { class: "two-col" },
          h("div", {}, h("h3", { class: "muted", style: { marginBottom: "8px" } }, "Reviewed"), reviews),
          h("div", {}, h("h3", { class: "muted", style: { marginBottom: "8px" } }, "Answered"), replies),
        ),
        h("div", { class: "card", style: { marginTop: "12px" } },
          h("div", { class: "card-head" }, h("h3", {}, "Seen by the watcher")),
          seen,
        ),
      ),
    ),
  );

  // --- data ---------------------------------------------------------------
  async function refresh() {
    try {
      const [execRes, histRes, statsRes, logsRes] = await Promise.all([
        awf("/api/executions"),
        awf("/api/history?limit=50"),
        awf("/api/history/stats"),
        getJSON("/api/logs").catch(() => []),
      ]);
      state.executions = execRes.body?.executions || [];
      state.runs = state.executions.filter((e) => e.status === "running");
      state.history = attachLogIds(histRes.body?.entries || [], logsRes || []);
      loadSessions();
      const s = statsRes.body || {};
      state.stats = { total: s.TotalExecutions || 0, failed: s.FailedCount || 0, avg: s.AvgDurationMs || 0 };
      renderKpis();
      renderRuns();
      renderHistory();
    } catch (err) {
      // app.js shows the awf banner; keep the last good data on screen.
      runsCount.textContent = "awf unreachable";
    }
  }

  // opencode charges per agent turn; the price lives in its own database.
  async function runCost(id, from, to) {
    const q = new URLSearchParams({ id, from });
    if (to) q.set("to", to);
    try {
      const c = await getJSON(`/api/cost?${q}`);
      return c.available ? c : null;
    } catch {
      return null;
    }
  }

  function money(c) {
    return c.cost >= 0.01 ? `$${c.cost.toFixed(3)}` : `$${c.cost.toFixed(4)}`;
  }

  // One query covers every row on the page; /api/costs returns the sessions in
  // the window, the page sums them per row.
  async function loadSessions() {
    const times = state.history.flatMap((e) => [Date.parse(e.started_at), Date.parse(e.completed_at) || 0])
      .filter((t) => t > 0);
    if (!times.length) return;
    const q = new URLSearchParams({
      from: new Date(Math.min(...times) - 60000).toISOString(),
      to: new Date(Math.max(...times) + 120000).toISOString(),
    });
    try {
      const res = await getJSON(`/api/costs?${q}`);
      state.sessions = res.available ? res.sessions || [] : [];
    } catch {
      state.sessions = [];
    }
    attributeSessions();
  }

  async function refreshArtifacts() {
    try {
      const data = await getJSON("/api/artifacts");
      state.artifacts = data;
      renderArtifacts();
    } catch {
      // Keep the previous artifacts; the header pill reports the backend.
    }
  }

  // awf history records carry their own id, not the execution id, so rows join
  // on workflow plus start second, the key the backend writes to
  // logs/index.jsonl. Two runs can start in the same second: take each id once.
  function attachLogIds(entries, logs) {
    const byKey = new Map();
    const add = (workflow, startedAt, id) => {
      const key = runKey(workflow, startedAt);
      byKey.set(key, [...(byKey.get(key) || []), id]);
    };
    for (const l of logs) add(l.workflow_name, l.started_at, l.execution_id);
    for (const e of state.executions) add(e.workflow_name, e.started_at, e.execution_id);
    return entries.map((e) => {
      const list = byKey.get(runKey(e.workflow_name, e.started_at));
      return { ...e, logId: list?.length ? list.shift() : undefined };
    });
  }

  // --- summary ------------------------------------------------------------
  function renderKpis() {
    const rows = [
      ["Live runs", state.runs.length, ""],
      ["Recorded runs", state.stats.total, ""],
      ["Failed runs", state.stats.failed, state.stats.failed ? "bad" : ""],
      ["Avg duration", dur(state.stats.avg), ""],
      ["PRs reviewed", state.artifacts.reviews.length, ""],
      ["PRs answered", state.artifacts.replies.length, ""],
    ];
    mount(kpis, rows.map(([label, value, tone]) =>
      h("div", { class: `kpi ${tone}` }, h("b", {}, String(value)), h("span", {}, label))));
  }

  // --- running runs -------------------------------------------------------
  function renderRuns() {
    runsCount.textContent = state.runs.length ? `${state.runs.length} running` : "nothing running";
    const alive = new Set(state.runs.map((e) => e.execution_id));
    for (const [id, entry] of runCards) {
      if (!alive.has(id)) {
        entry.stream?.close();
        entry.card.remove();
        runCards.delete(id);
      }
    }
    if (!state.runs.length) {
      mount(runs, h("div", { class: "empty" }, "No workflow running. The watcher polls every two minutes; the daily pass runs on its timer."));
      return;
    }
    if (runs.querySelector(".empty")) runs.replaceChildren();
    for (const run of state.runs) {
      let entry = runCards.get(run.execution_id);
      if (!entry) {
        entry = createRunCard(run);
        runCards.set(run.execution_id, entry);
        runs.appendChild(entry.card);
      }
      entry.step.textContent = run.current_step || "starting";
      entry.updateCost();
      entry.time.textContent = elapsed(run.started_at);
      const pre = entry.pre;
      const text = logText.get(run.execution_id);
      if (text && pre.textContent !== text) pre.textContent = text;
    }
  }

  function createRunCard(run) {
    const step = h("span", { class: "step muted" }, "starting");
    const costEl = h("span", { class: "cost muted num" });
    const time = h("span", { class: "time muted num" });
    const pre = h("pre", { class: "log" }, logText.get(run.execution_id) || "connecting to the stream…");
    const card = h("div", { class: "run" },
      h("div", { class: "row" },
        h("b", {}, run.workflow_name),
        h("span", { class: "badge running" }, "running"),
        step,
        costEl,
        h("span", { class: "spacer" }),
        time,
        h("button", {
          class: "btn danger sm",
          onclick: () => cancelRun(run),
        }, "Cancel"),
      ),
      h("div", { class: "bar", role: "progressbar", "aria-label": "Run in progress" }, h("i")),
      pre,
    );
    const stream = follow(run.execution_id, (text) => {
      logText.set(run.execution_id, text);
      if (pre.isConnected) {
        pre.textContent = text;
        pre.scrollTop = pre.scrollHeight;
      }
    });
    const updateCost = async () => {
      const c = await runCost(run.execution_id, run.started_at);
      if (!c || !c.cost) return;
      costEl.textContent = money(c);
      costEl.title = `${c.input.toLocaleString()} in · ${c.output.toLocaleString()} out · ${c.cache_read.toLocaleString()} cached`;
    };
    updateCost();
    return { card, step, costEl, time, pre, stream, updateCost };
  }

  // The backend records awf's single event stream, so the page tails that file
  // instead of opening awf's stream: a second reader would steal events from the
  // recorder. Tailing also replays the steps that ran before the page opened.
  function follow(id, paint) {
    let text = logText.get(id) || "";
    let poll = null;
    const es = new EventSource(`/api/stream?id=${encodeURIComponent(id)}`);
    es.onmessage = (ev) => {
      let data;
      try { data = JSON.parse(ev.data); } catch { return; }
      const kind = data.kind || "event";
      text = trimLines(text + eventLine({ ...data, kind }) + "\n", MAX_LOG_LINES);
      paint(text);
      if (kind === "workflow.completed" || kind === "workflow.failed") {
        es.close();
        clearInterval(poll);
        setTimeout(refresh, 800);
      }
    };
    es.onerror = () => {
      if (es.readyState !== EventSource.CLOSED) return;
      es.close();
      if (poll) return;
      poll = setInterval(async () => {
        try {
          text = formatLog(await getText(`/api/log?id=${encodeURIComponent(id)}`));
          paint(text);
        } catch { /* the recorder may not have written yet */ }
      }, 2500);
    };
    return {
      close() { es.close(); clearInterval(poll); },
    };
  }

  async function cancelRun(run) {
    const ok = await confirmDialog({
      title: `Cancel ${run.workflow_name}?`,
      message: "awf stops the run at the next step boundary. Finished steps stay on disk.",
      confirm: "Cancel the run",
      danger: true,
    });
    if (!ok) return;
    try {
      await awf(`/api/executions/${encodeURIComponent(run.execution_id)}`, { method: "DELETE" });
      toast(`Cancelled ${run.workflow_name}`, { tone: "ok" });
      refresh();
    } catch (err) {
      toast(`awf refused the cancel: ${err.message}`, { tone: "err" });
    }
  }

  // A session usually starts inside the run that launched it. Runs recorded
  // back to back overlap a little, so when several rows match, the longer run
  // keeps it: a probe that lasted milliseconds cannot have launched a turn.
  // Sessions outside every run window fall back to a wider window, because a
  // step may prepare for a while before the agent starts.
  function attributeSessions() {
    const buckets = state.history.map((e) => {
      const start = Date.parse(e.started_at);
      const end = e.completed_at ? Date.parse(e.completed_at) : start;
      return {
        key: rowKey(e),
        from: start - 5000,
        to: end + 5000,
        looseFrom: start - 60000,
        looseTo: end + 180000,
        len: end - start,
        cost: 0,
      };
    });
    const claim = (s, loose) => {
      let best = null;
      for (const b of buckets) {
        const from = loose ? b.looseFrom : b.from;
        const to = loose ? b.looseTo : b.to;
        if (s.at < from || s.at > to) continue;
        if (!best || b.len > best.len) best = b;
      }
      return best;
    };
    for (const s of state.sessions) {
      const best = claim(s, false) || claim(s, true);
      if (best) best.cost += s.cost;
    }
    state.costs = new Map(buckets.map((b) => [b.key, b.cost]));
  }

  function rowKey(e) {
    return `${e.workflow_name}|${e.started_at}`;
  }

  function costOf(e) {
    return state.costs.get(rowKey(e)) || 0;
  }

  function costCell(e) {
    const c = costOf(e);
    return c ? money({ cost: c }) : h("span", { class: "muted" }, "—");
  }

  function renderHistory() {
    const rows = state.history.filter((e) =>
      (!state.filter || (e.workflow_name || "").toLowerCase().includes(state.filter)) &&
      (state.status === "all" || e.status === state.status));
    historyCount.textContent = `${rows.length} of ${state.history.length}`;
    if (!rows.length) {
      mount(historyBody, h("tr", {}, h("td", { colspan: "7" }, h("div", { class: "empty" },
        state.history.length ? "No run matches the filter." : "No finished run recorded yet."))));
      return;
    }
    mount(historyBody, rows.map((e) => h("tr", {},
      h("td", {}, h("b", {}, e.workflow_name)),
      h("td", {}, h("span", { class: `badge ${e.status}` }, e.status)),
      h("td", { class: "num muted", title: when(e.started_at) }, ago(e.started_at)),
      h("td", { class: "num" }, dur(e.duration_ms)),
      h("td", { class: "num" }, costCell(e)),
      h("td", {}, e.logId
        ? h("button", {
          class: "btn ghost sm mono",
          title: `${e.logId} (click to copy)`,
          onclick: () => copyId(e.logId),
        }, e.logId.slice(0, 8))
        : h("span", { class: "muted" }, "—")),
      h("td", { class: "actions" },
        e.logId
          ? h("button", { class: "btn sm", onclick: () => showLog(e) }, "Log")
          : h("span", { class: "muted" }, "—"),
        h("button", { class: "btn ghost sm", title: `Run ${e.workflow_name} again`, onclick: () => replay(e) }, "Replay"),
      ),
    )));
  }

  async function copyId(id) {
    try {
      await navigator.clipboard.writeText(id);
      toast(`Run id copied: ${id}`, { tone: "info" });
    } catch {
      toast(`Run id: ${id}`, { tone: "info", timeout: 15000 });
    }
  }

  // replay opens the run dialog of the same workflow. The inputs come from the
  // page's own record when it started the run, otherwise from the run log.
  async function replay(entry) {
    let info = { workflow: entry.workflow_name, scope: "global", inputs: {}, source: "none" };
    try {
      const q = new URLSearchParams({ workflow: entry.workflow_name || "" });
      if (entry.logId) q.set("id", entry.logId);
      info = await getJSON(`/api/replay?${q}`);
    } catch (err) {
      toast(`Cannot read what this run was given: ${err.message}`, { tone: "err" });
    }
    const recalled = recalledRun(entry.logId);
    const prefill = recalled?.inputs || info.inputs || {};
    let detail;
    try {
      detail = (await awf(`/api/workflows/${encodeURIComponent(info.scope)}/${encodeURIComponent(info.workflow)}`)).body;
    } catch (err) {
      toast(`Cannot load ${info.workflow}: ${err.message}`, { tone: "err" });
      return;
    }
    const note = recalled
      ? "Same inputs as the run you started from this browser. Check them before running."
      : info.source === "log"
        ? "These inputs are read back from the run log, not stored by awf. Check them before running."
        : "No record of what this run was given. Fill the inputs in.";
    openRunModal({
      scope: info.scope,
      name: info.workflow,
      inputs: detail.Inputs || [],
      prefill,
      note,
      onStarted: (id) => {
        toast(`Run started: ${String(id).slice(0, 8)}`, { tone: "ok" });
        refresh();
      },
    });
  }

  async function showLog(entry) {
    const pre = h("pre", { style: { maxHeight: "60vh" } }, "loading…");
    const modal = openModal({
      title: `${entry.workflow_name} · ${when(entry.started_at)} · ${entry.logId ? entry.logId.slice(0, 8) : "no id"}`,
      body: pre,
    });
    try {
      const raw = await getText(`/api/stream?id=${encodeURIComponent(entry.logId)}`);
      pre.textContent = formatLog(raw) || "The log is empty.";
      const c = await runCost(entry.logId, entry.started_at, entry.completed_at);
      if (c && c.cost) modal.querySelector("h2").textContent += ` · ${money(c)}`;
    } catch (err) {
      pre.textContent = `No log on disk for this run (${err.message}). The recorder only keeps runs seen while the dashboard runs.`;
    }
    return modal;
  }

  function renderArtifacts() {
    const a = state.artifacts;
    mount(reviews, a.reviews.length ? a.reviews.map((r) => h("div", { class: "item" },
      h("div", {}, h("b", {}, r.title || "untitled pull request")),
      h("div", { class: "meta" },
        `${r.author || "unknown author"}${r.base ? ` → ${r.base}` : ""} · ${r.diff_lines} diff lines · ${r.has_review ? "review written" : "no review yet"}`),
      r.has_review ? artifactDetails(`reviews/${r.id}/review.md`, "review") : null,
      artifactDetails(`reviews/${r.id}/diff.patch`, "diff"),
    )) : h("div", { class: "empty" }, "No pull request reviewed yet."));

    mount(replies, a.replies.length ? a.replies.map((r) => h("div", { class: "item" },
      h("div", {}, h("b", {}, r.title || "untitled pull request")),
      h("div", { class: "meta" },
        `${r.author || "unknown author"}${r.branch ? ` on ${r.branch}` : ""} · ${r.comments} comment${r.comments === 1 ? "" : "s"}`,
        r.pushed ? ` · pushed ${r.pushed.slice(0, 8)}` : "",
      ),
      r.url ? h("div", { class: "meta" }, h("a", { href: r.url, target: "_blank", rel: "noreferrer" }, "Open on GitHub")) : null,
      r.has_reply ? artifactDetails(`replies/${r.id}/reply.md`, "reply") : null,
      artifactDetails(`replies/${r.id}/diff.patch`, "diff"),
    )) : h("div", { class: "empty" }, "No reply drafted yet."));

    mount(seen, a.seen.length
      ? h("div", { class: "chips" }, a.seen.slice().reverse().slice(0, 200).map((s) =>
        h("span", { class: "chip" }, `${s.repo} `, h("b", {}, `#${s.pr}`))))
      : h("div", { class: "empty" }, "The watcher has not marked anything as seen."));
  }

  function artifactDetails(path, label) {
    const pre = h("pre", {}, fileCache.get(path) ?? "loading…");
    const details = h("details", {},
      h("summary", {}, label),
      pre,
    );
    details.addEventListener("toggle", async () => {
      if (!details.open || fileCache.has(path)) return;
      try { fileCache.set(path, await getText(`/api/file?path=${encodeURIComponent(path)}`)); }
      catch (err) { fileCache.set(path, `Cannot read ${path}: ${err.message}`); }
      pre.textContent = fileCache.get(path);
    });
    return details;
  }

  // --- lifecycle ----------------------------------------------------------
  const tick = setInterval(() => {
    for (const run of state.runs) {
      const entry = runCards.get(run.execution_id);
      if (entry) entry.time.textContent = elapsed(run.started_at);
    }
  }, 1000);
  const timer = setInterval(refresh, REFRESH_MS);
  const artTimer = setInterval(refreshArtifacts, ARTIFACTS_MS);

  refresh();
  refreshArtifacts();

  return {
    destroy() {
      clearInterval(tick);
      clearInterval(timer);
      clearInterval(artTimer);
      for (const entry of runCards.values()) entry.stream?.close();
    },
  };
}

function runKey(workflow, startedAt) {
  return `${workflow}|${Math.floor(Date.parse(startedAt) / 1000)}`;
}

function trimLines(text, max) {
  const lines = text.split("\n");
  return lines.length <= max ? text : lines.slice(lines.length - max).join("\n");
}
