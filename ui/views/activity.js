import { h, mount } from "../dom.js";
import { awf, getJSON, getText } from "../api.js";
import { ago, dur, elapsed, eventLine, formatLog, when } from "../format.js";
import { toast } from "../toast.js";
import { confirmDialog, openModal } from "../modal.js";
import { openRunModal, recalledRun } from "../run.js";
import { openAskModal } from "../ask.js";
import { stepLabel, trailText } from "../steps.js";

const REFRESH_MS = 5000;
const ARTIFACTS_MS = 15000;
const MAX_LOG_LINES = 800;

export function mountActivity(el) {
  const state = {
    executions: [],
    history: [],
    stats: { total: 0, failed: 0, avg: 0 },
    runs: [],
    targets: new Map(),
    routing: new Map(),
    filter: "",
    status: "all",
    page: 0,
    reviewsDone: false,
    repliesDone: false,
    artifacts: { reviews: [], replies: [], seen: [] },
  };
  const PAGE = 10; // history rows per page
  const PR_LIMIT = 5; // pull request cards shown before folding
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
  const pager = h("div", { class: "pager" });

  const search = h("input", {
    type: "search",
    placeholder: "Filter by workflow",
    "aria-label": "Filter history by workflow",
    oninput: () => { state.filter = search.value.trim().toLowerCase(); state.page = 0; renderHistory(); },
  });
  const status = h("select", {
    "aria-label": "Filter history by status",
    onchange: () => { state.status = status.value; state.page = 0; renderHistory(); },
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
              h("th", {}, "Duration"), h("th", {}, "Models"), h("th", {}, "Cost"), h("th", {}, "Run id"),
              h("th", { class: "actions" }, "Log"),
            )),
            historyBody,
          ),
        ),
        pager,
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
      state.targets = new Map((logsRes || []).map((l) => [l.execution_id, targetOf(l)]));
      state.routing = new Map((logsRes || []).filter((l) => l.provider).map((l) => [l.execution_id, { provider: l.provider, route: l.route }]));
      state.history = attachLogIds(histRes.body?.entries || [], logsRes || []);
      const s = statsRes.body || {};
      state.stats = { total: s.TotalExecutions || 0, failed: s.FailedCount || 0, avg: s.AvgDurationMs || 0 };
      renderKpis();
      renderRuns();
      renderHistory();
    } catch {
      // app.js shows the awf banner; keep the last good data on screen.
      runsCount.textContent = "awf unreachable";
    }
  }

  // awf records what each agent step cost; the backend sums it per run.
  async function runCost(id) {
    const q = new URLSearchParams({ id });
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

  async function refreshArtifacts() {
    try {
      const data = await getJSON("/api/artifacts");
      state.artifacts = data;
      renderArtifacts();
      renderKpis();
    } catch {
      // Keep the previous artifacts; the header pill reports the backend.
    }
  }

  // awf history records carry their own id, not the execution id, so rows join
  // on workflow plus start second, the key the backend writes to
  // logs/index.jsonl. Two runs can start in the same second: take each id once.
  function attachLogIds(entries, logs) {
    const byKey = new Map();
    const costs = new Map();
    const roundsById = new Map();
    const models = new Map();
    const wanted = new Map();
    for (const e of entries) {
      const key = runKey(e.workflow_name, e.started_at);
      wanted.set(key, (wanted.get(key) || 0) + 1);
    }
    const add = (workflow, startedAt, id, cost, rounds) => {
      const key = runKey(workflow, startedAt);
      const ids = byKey.get(key) || [];
      if (!ids.includes(id)) byKey.set(key, [...ids, id]);
      if (cost) costs.set(id, cost);
      if (rounds?.length) roundsById.set(id, rounds);
    };
    for (const l of logs) {
      add(l.workflow_name, l.started_at, l.execution_id, l.cost, l.rounds);
      if (l.models) models.set(l.execution_id, l.models);
    }
    for (const e of state.executions) add(e.workflow_name, e.started_at, e.execution_id, 0);
    return entries.map((e) => {
      const key = runKey(e.workflow_name, e.started_at);
      const list = byKey.get(key);
      const logId = list?.length && list.length === wanted.get(key) ? list.shift() : undefined;
      wanted.set(key, wanted.get(key) - 1);
      return { ...e, logId, rounds: (logId && roundsById.get(logId)) || [], cost: (logId && costs.get(logId)) || 0, target: (logId && state.targets.get(logId)) || "", models: (logId && models.get(logId)) || null, routing: (logId && state.routing.get(logId)) || null };
    });
  }

  // targetOf names the run target from the fields the backend parsed out of
  // the log: "owner/repo#123" for a review, "owner/repo#7" for an issue.
  function targetOf(row) {
    const num = row.pr || row.issue;
    return row.repo && num ? `${row.repo}#${num}` : "";
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
      const trail = trailText(run.workflow_name, entry.steps);
      entry.step.textContent = trail || (run.current_step ? stepLabel(run.workflow_name, run.current_step) : "starting");
      entry.step.title = run.current_step ? `current step: ${run.current_step}` : "";
      entry.target.textContent = state.targets.get(run.execution_id) || "";
      mount(entry.routing, routingChips(state.routing.get(run.execution_id)));
      entry.updateCost();
      entry.time.textContent = elapsed(run.started_at);
      const pre = entry.pre;
      const text = logText.get(run.execution_id);
      if (text && pre.textContent !== text) pre.textContent = text;
    }
  }

  function createRunCard(run) {
    const step = h("span", { class: "step muted" }, "starting");
    const target = h("span", { class: "muted", title: "target guessed from the run log" }, state.targets.get(run.execution_id) || "");
    const routing = h("span", { class: "chips" }, routingChips(state.routing.get(run.execution_id)));
    const costEl = h("span", { class: "cost muted num" });
    const time = h("span", { class: "time muted num" });
    const pre = h("pre", { class: "log" }, logText.get(run.execution_id) || "connecting to the stream…");
    const card = h("div", { class: "run" },
      h("div", { class: "row" },
        h("b", {}, run.workflow_name),
        target,
        routing,
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
    const steps = [];
    const stream = follow(run.execution_id, (text) => {
      logText.set(run.execution_id, text);
      if (pre.isConnected) {
        pre.textContent = text;
        pre.scrollTop = pre.scrollHeight;
      }
    }, (name) => {
      if (name === null) steps.length = 0;
      else steps.push(name);
      step.textContent = trailText(run.workflow_name, steps);
    });
    const updateCost = async () => {
      const c = await runCost(run.execution_id);
      if (!c?.cost) return;
      costEl.textContent = money(c);
      costEl.title = `${c.steps} agent step${c.steps === 1 ? "" : "s"} so far`;
    };
    updateCost();
    return { card, step, steps, target, routing, costEl, time, pre, stream, updateCost };
  }

  // The backend records awf's single event stream, so the page tails that file
  // instead of opening awf's stream: a second reader would steal events from the
  // recorder. Tailing also replays the steps that ran before the page opened.
  function follow(id, paint, onStep) {
    let text = logText.get(id) || "";
    let poll = null;
    const es = new EventSource(`/api/stream?id=${encodeURIComponent(id)}`);
    // A reconnect replays the run from its first event: start the trail over.
    es.onopen = () => onStep?.(null);
    es.onmessage = (ev) => {
      let data;
      try { data = JSON.parse(ev.data); } catch { return; }
      const kind = data.kind || "event";
      if (kind === "step.started" && data.step) onStep?.(data.step);
      text = trimLines(`${text + eventLine({ ...data, kind })}\n`, MAX_LOG_LINES);
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

  function costOf(e) {
    return e.cost || 0;
  }

  function costCell(e) {
    const c = costOf(e);
    return c ? money({ cost: c }) : h("span", { class: "muted" }, "—");
  }

  // routingChips names the agent CLI a run used and, for fix-issue, whether the
  // token could push to the repository ("owned") or the branch went through a
  // fork ("external"). A run with no recorded state shows nothing.
  function routingChips(routing) {
    if (!routing?.provider) return [];
    const chips = [h("span", { class: `chip provider-${routing.provider}`, title: "agent CLI the steps ran on" }, routing.provider)];
    if (routing.route) {
      chips.push(h("span", {
        class: "chip",
        title: routing.route === "external" ? "the token cannot push here: worked through a fork" : "the token can push here",
      }, routing.route));
    }
    return chips;
  }

  function modelCell(models, routing) {
    const pairs = models && Object.entries(models);
    const chips = [
      ...routingChips(routing),
      ...(pairs || []).map(([key, value]) =>
        h("span", { class: "chip", title: `${key}=${value}` }, `${key.replace(/_model$/, "")}: ${value}`)),
    ];
    return h("td", {}, chips.length
      ? h("span", { class: "chips" }, chips)
      : h("span", { class: "muted" }, "—"));
  }

  // renderPager keeps the newest runs first and lets the table stay compact.
  function renderPager(total, pages) {
    if (total <= PAGE) {
      mount(pager);
      return;
    }
    const from = state.page * PAGE + 1;
    const to = Math.min(total, (state.page + 1) * PAGE);
    mount(pager,
      h("span", { class: "muted" }, `${from}–${to} of ${total}`),
      h("span", { class: "spacer" }),
      h("button", {
        class: "btn ghost sm",
        disabled: state.page === 0,
        onclick: () => { state.page = Math.max(0, state.page - 1); renderHistory(); },
      }, "Newer"),
      h("span", { class: "muted num" }, `${state.page + 1} / ${pages}`),
      h("button", {
        class: "btn ghost sm",
        disabled: state.page >= pages - 1,
        onclick: () => { state.page = Math.min(pages - 1, state.page + 1); renderHistory(); },
      }, "Older"),
    );
  }

  function renderHistory() {
    const rows = state.history.filter((e) =>
      (!state.filter || (e.workflow_name || "").toLowerCase().includes(state.filter)) &&
      (state.status === "all" || e.status === state.status));
    historyCount.textContent = `${rows.length} of ${state.history.length}`;
    const pages = Math.max(1, Math.ceil(rows.length / PAGE));
    state.page = Math.min(state.page, pages - 1);
    const shown = rows.slice(state.page * PAGE, state.page * PAGE + PAGE);
    renderPager(rows.length, pages);
    if (!shown.length) {
      mount(historyBody, h("tr", {}, h("td", { colspan: "8" }, h("div", { class: "empty" },
        state.history.length ? "No run matches the filter." : "No finished run recorded yet."))));
      return;
    }
    mount(historyBody, shown.map((e) => h("tr", {},
      h("td", {},
        h("b", {}, e.workflow_name),
        e.target ? h("span", { class: "muted", title: "target guessed from the run log" }, ` ${e.target}`) : null),
      h("td", {}, h("span", { class: `badge ${e.status}` }, e.status)),
      h("td", { class: "num muted", title: when(e.started_at) }, ago(e.started_at)),
      h("td", { class: "num" }, dur(e.duration_ms)),
      modelCell(e.models, e.routing),
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

  // The timeline answers "why did this run take seven minutes": awf stamps
  // every step, so the split between a slow agent step and a slow command is
  // measured, not guessed. The raw log stays below it.
  async function timeline(id) {
    let t;
    try {
      t = await getJSON(`/api/run?id=${encodeURIComponent(id)}`);
    } catch {
      return null;
    }
    if (!t.steps?.length) return null;
    let sess = [];
    try {
      sess = (await getJSON(`/api/sessions?id=${encodeURIComponent(id)}`)).sessions || [];
    } catch { /* the sessions column is optional */ }
    const sessionCell = (name) => h("td", { class: "muted" }, sess.filter((x) => x.step === name).map((x) =>
      h("div", { title: x.id },
        `${x.id.slice(-8)} ${x.model || ""} · ${x.tool_calls} tools · ${x.output_tokens} out `,
        x.loop ? h("span", { class: "badge failed" }, "loop") : null,
        x.orphan ? h("span", { class: "badge failed" }, "orphan") : null)));
    const rows = t.steps.map((s) => h("tr", {},
      h("td", {}, s.name),
      h("td", { class: "muted" }, s.kind),
      h("td", { class: "muted" }, s.error ? h("span", { class: "badge failed" }, "error") : s.status),
      h("td", { class: "num" }, dur(s.ms)),
      h("td", { class: "num" }, s.cost ? money(s) : "—"),
      h("td", { class: "num muted" }, s.prompt ? `${s.prompt} → ${s.answer}` : ""),
      sessionCell(s.name),
    ));
    return h("div", { class: "table-wrap", style: { marginBottom: "12px" } },
      h("table", {},
        h("thead", {}, h("tr", {},
          h("th", {}, "Step"), h("th", {}, "Kind"), h("th", {}, "Status"),
          h("th", { class: "num" }, "Time"), h("th", { class: "num" }, "Cost"),
          h("th", { class: "num", title: "prompt bytes → answer bytes" }, "Prompt"),
          h("th", { title: "opencode session behind the step" }, "Session"),
        )),
        h("tbody", {}, rows),
      ),
    );
  }

  async function showLog(entry) {
    const pre = h("pre", { style: { maxHeight: "60vh" } }, "loading…");
    const slot = h("div", {});
    const rounds = h("div", {});
    const modal = openModal({
      title: `${entry.workflow_name} · ${when(entry.started_at)} · ${entry.logId ? entry.logId.slice(0, 8) : "no id"}`,
      body: h("div", {}, slot, rounds, pre),
    });
    if (entry.rounds?.length) {
      mount(rounds, entry.rounds.flatMap((p) => {
        const n = /diff\.round(\d+)\.patch$/.exec(p)?.[1];
        return n ? [artifactDetails(p, `diff round ${n}`)] : [];
      }));
    }
    if (entry.logId) {
      const tl = await timeline(entry.logId);
      if (tl) mount(slot, tl);
    }
    try {
      const raw = await getText(`/api/stream?id=${encodeURIComponent(entry.logId)}`);
      pre.textContent = formatLog(raw) || "The log is empty.";
      const c = await runCost(entry.logId);
      if (c?.cost) modal.querySelector("h2").textContent += ` · ${money(c)}`;
    } catch (err) {
      pre.textContent = `No log on disk for this run (${err.message}). The recorder only keeps runs seen while the dashboard runs.`;
    }
    return modal;
  }

  // A pulled request awf-gh already merged or that someone closed keeps its card
  // on disk; the page folds those away instead of listing them forever. The
  // state is what reply-pr saw when it last fetched, so a pull request merged
  // after that fetch stays listed until a run fetches it again.
  function isDone(state) {
    return state === "MERGED" || state === "CLOSED";
  }

  function stateBadge(state) {
    if (!isDone(state)) return null;
    return h("span", { class: `badge ${state === "MERGED" ? "completed" : "cancelled"}`, style: { marginLeft: "8px" } }, state.toLowerCase());
  }

  function reviewCard(r) {
    return h("div", { class: "item" },
      h("div", {}, h("b", {}, r.title || "untitled pull request"), stateBadge(r.state)),
      h("div", { class: "meta" },
        `${r.author || "unknown author"}${r.base ? ` → ${r.base}` : ""} · ${r.diff_lines} diff lines · ${r.has_review ? "review written" : "no review yet"}`),
      r.url ? h("div", { class: "meta" }, h("a", { href: r.url, target: "_blank", rel: "noreferrer" }, "Open on GitHub")) : null,
      r.has_review ? artifactDetails(`reviews/${r.id}/review.md`, "review") : null,
      artifactDetails(`reviews/${r.id}/diff.patch`, "diff"),
      askButton(r),
    );
  }

  // foldedList keeps the pull requests that still need attention visible and
  // puts the merged and closed ones behind one button.
  function foldedList(items, card, emptyText, keys) {
    const active = items.filter((x) => !isDone(x.state));
    const done = items.filter((x) => isDone(x.state));
    return [
      active.length
        ? cardList(active, state[keys.open], () => { state[keys.open] = !state[keys.open]; renderArtifacts(); }, card)
        : h("div", { class: "empty" }, items.length ? "Every pull request here is merged or closed." : emptyText),
      done.length
        ? h("button", {
          class: "btn ghost sm",
          onclick: () => { state[keys.done] = !state[keys.done]; renderArtifacts(); },
        }, state[keys.done] ? "Hide merged and closed" : `Show ${done.length} merged or closed`)
        : null,
      state[keys.done] ? done.map(card) : null,
    ];
  }

  function replyCard(r) {
    return h("div", { class: "item" },
      h("div", {}, h("b", {}, r.title || "untitled pull request"), stateBadge(r.state)),
      h("div", { class: "meta" },
        `${r.author || "unknown author"}${r.branch ? ` on ${r.branch}` : ""} · ${r.comments} comment${r.comments === 1 ? "" : "s"}`,
        r.pushed ? ` · pushed ${r.pushed.slice(0, 8)}` : "",
      ),
      r.url ? h("div", { class: "meta" }, h("a", { href: r.url, target: "_blank", rel: "noreferrer" }, "Open on GitHub")) : null,
      r.has_reply ? artifactDetails(`replies/${r.id}/reply.md`, "reply") : null,
      artifactDetails(`replies/${r.id}/diff.patch`, "diff"),
      askButton(r),
    );
  }

  // cardList shows the newest few cards and folds the rest behind a button:
  // the pull request lists grow without bound and would push History off screen.
  function cardList(items, open, toggle, render) {
    const shown = open ? items : items.slice(0, PR_LIMIT);
    const rest = items.length - shown.length;
    return [
      ...shown.map(render),
      items.length > PR_LIMIT
        ? h("button", { class: "btn ghost sm", onclick: toggle },
          rest > 0 ? `Show ${rest} more` : "Show fewer")
        : null,
    ];
  }

  function renderArtifacts() {
    const a = state.artifacts;
    mount(reviews, foldedList(a.reviews, reviewCard, "No pull request reviewed yet.", { open: "reviewsOpen", done: "reviewsDone" }));
    mount(replies, foldedList(a.replies, replyCard, "No reply drafted yet.", { open: "repliesOpen", done: "repliesDone" }));

    mount(seen, a.seen.length
      ? h("div", { class: "chips" }, a.seen.slice().reverse().slice(0, 200).map((s) =>
        h("span", { class: "chip" }, `${s.repo} `, h("b", {}, `#${s.pr}`))))
      : h("div", { class: "empty" }, "The watcher has not marked anything as seen."));
  }

  // askButton opens the conversation for this pull request. The repository is
  // unknown for artifacts written before workflows/ review-pr started recording
  // it, so those cards get no button instead of a wrong one.
  function askButton(item) {
    if (!item.repo || !item.pr) return null;
    return h("div", { class: "meta" },
      h("button", {
        class: "btn sm",
        title: `Ask the agent about ${item.repo}#${item.pr}`,
        onclick: () => openAskModal({ repo: item.repo, pr: item.pr, title: item.title }),
      }, "Ask"));
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
