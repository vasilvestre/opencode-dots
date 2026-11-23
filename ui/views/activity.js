import { h, mount } from "../dom.js";
import { awf, getJSON, getText } from "../api.js";
import { ago, dur, elapsed, formatLog, when } from "../format.js";
import { toast } from "../toast.js";
import { confirmDialog, openModal } from "../modal.js";

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
              h("th", {}, "Duration"), h("th", { class: "actions" }, "Log"),
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

  async function refreshArtifacts() {
    try {
      const data = await getJSON("/api/artifacts");
      state.artifacts = data;
      renderArtifacts();
    } catch {
      // Keep the previous artifacts; the header pill reports the backend.
    }
  }

  // awf history ids differ from execution ids, so rows join on workflow plus
  // start second, the same key the backend records in logs/index.jsonl.
  function attachLogIds(entries, logs) {
    const byKey = new Map(logs.map((l) => [runKey(l.workflow_name, l.started_at), l.execution_id]));
    for (const e of state.executions) {
      const key = runKey(e.workflow_name, e.started_at);
      if (!byKey.has(key)) byKey.set(key, e.execution_id);
    }
    return entries.map((e) => ({ ...e, logId: byKey.get(runKey(e.workflow_name, e.started_at)) }));
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
      entry.time.textContent = elapsed(run.started_at);
      const pre = entry.pre;
      const text = logText.get(run.execution_id);
      if (text && pre.textContent !== text) pre.textContent = text;
    }
  }

  function createRunCard(run) {
    const step = h("span", { class: "step muted" }, "starting");
    const time = h("span", { class: "time muted num" });
    const pre = h("pre", { class: "log" }, logText.get(run.execution_id) || "connecting to the stream…");
    const card = h("div", { class: "run" },
      h("div", { class: "row" },
        h("b", {}, run.workflow_name),
        h("span", { class: "badge running" }, "running"),
        step,
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
    return { card, step, time, pre, stream };
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
      const line = `${String(data.seq ?? "").padStart(3)} ${data.step || "-"} ${kind}` +
        (data.status ? ` (${data.status})` : "") +
        (data.error ? ` error: ${data.error}` : "") +
        (data.prompt ? `\n    ${String(data.prompt).slice(0, 300).replace(/\n/g, "\n    ")}` : "");
      text = trimLines(text + line + "\n", MAX_LOG_LINES);
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

  function renderHistory() {
    const rows = state.history.filter((e) =>
      (!state.filter || (e.workflow_name || "").toLowerCase().includes(state.filter)) &&
      (state.status === "all" || e.status === state.status));
    historyCount.textContent = `${rows.length} of ${state.history.length}`;
    if (!rows.length) {
      mount(historyBody, h("tr", {}, h("td", { colspan: "5" }, h("div", { class: "empty" },
        state.history.length ? "No run matches the filter." : "No finished run recorded yet."))));
      return;
    }
    mount(historyBody, rows.map((e) => h("tr", {},
      h("td", {}, h("b", {}, e.workflow_name)),
      h("td", {}, h("span", { class: `badge ${e.status}` }, e.status)),
      h("td", { class: "num muted", title: when(e.started_at) }, ago(e.started_at)),
      h("td", { class: "num" }, dur(e.duration_ms)),
      h("td", { class: "actions" },
        e.logId
          ? h("button", { class: "btn sm", onclick: () => showLog(e) }, "Log")
          : h("span", { class: "muted" }, "—"),
      ),
    )));
  }

  async function showLog(entry) {
    const pre = h("pre", { style: { maxHeight: "60vh" } }, "loading…");
    const modal = openModal({
      title: `${entry.workflow_name} · ${when(entry.started_at)}`,
      body: pre,
    });
    try {
      const raw = await getText(`/api/log?id=${encodeURIComponent(entry.logId)}`);
      pre.textContent = formatLog(raw) || "The log is empty.";
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
