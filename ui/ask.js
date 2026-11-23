import { h, richText } from "./dom.js";
import { getJSON, postJSON } from "./api.js";
import { when } from "./format.js";
import { openModal } from "./modal.js";
import { openRunModal } from "./run.js";
import { toast } from "./toast.js";

// One conversation per pull request: the operator writes, the ask-pr workflow
// answers, and the page keeps the exchange on disk so a reload finds it again.
// The agent reads the same thread before answering, so it remembers the turns.
export async function openAskModal({ repo, pr, title }) {
  const log = h("div", { class: "chat-log", "aria-live": "polite" });
  const status = h("p", { class: "chat-status muted", hidden: true }, "");
  const input = h("textarea", {
    class: "chat-input",
    rows: 3,
    placeholder: `Ask about ${repo}#${pr}…`,
    "aria-label": "Message to the agent",
  });
  const send = h("button", { class: "btn primary", type: "submit" }, "Send");
  const form = h("form", { class: "composer", onsubmit: (ev) => { ev.preventDefault(); dispatch(); } },
    input, send);
  const body = h("div", { class: "chat" },
    h("div", { class: "chat-head" },
      h("b", {}, `${repo}#${pr}`),
      h("span", { class: "muted" }, title || ""),
    ),
    log, status, form);

  const modal = openModal({ title: `Ask · ${repo}#${pr}`, body, width: "760px" });

  let busy = false;
  let stream = null;

  function bubble(role, text, extra) {
    const el = h("div", { class: `bubble ${role}` },
      h("div", { class: "bubble-text" }, richText(text)),
      extra || null);
    log.append(el);
    log.scrollTop = log.scrollHeight;
    return el;
  }

  // answerExtra is what follows an answer: when it was written, what it cost,
  // and the workflows the agent proposed. A button opens the normal run dialog
  // with the pull request filled in; the operator still confirms the inputs.
  function answerExtra(m) {
    const rows = [];
    const bits = [];
    if (m.at) bits.push(when(m.at));
    if (m.cost) bits.push(`$${m.cost.toFixed(4)}`);
    if (bits.length) rows.push(h("div", { class: "bubble-meta muted" }, bits.join(" · ")));
    if (m.suggest?.length) {
      rows.push(h("div", { class: "suggest" },
        h("span", { class: "muted" }, "Next step:"),
        m.suggest.map((sg) => h("button", {
          class: "btn sm",
          type: "button",
          title: `Start ${sg.workflow} for ${repo}#${pr}`,
          onclick: () => runSuggested(sg),
        }, `Run ${sg.workflow}`)),
      ));
    }
    return rows.length ? h("div", {}, rows) : null;
  }

  // runSuggested resolves the workflow's scope from awf, then reuses the run
  // dialog: nothing starts without the operator pressing Run now.
  async function runSuggested(sg) {
    try {
      const list = await getJSON("/awf/api/workflows");
      const wf = (list.body?.workflows || []).find((w) => w.name === sg.workflow);
      if (!wf) {
        toast(`awf has no workflow named ${sg.workflow}`, { tone: "err" });
        return;
      }
      const detail = await getJSON(`/awf/api/workflows/${encodeURIComponent(wf.scope)}/${encodeURIComponent(wf.name)}`);
      openRunModal({
        scope: wf.scope,
        name: wf.name,
        inputs: detail.body?.Inputs || [],
        prefill: { repo, pr, ...(sg.inputs || {}) },
        note: "Suggested by the agent from this conversation. Check the inputs before running.",
      });
    } catch (err) {
      toast(`Cannot open ${sg.workflow}: ${err.message}`, { tone: "err" });
    }
  }

  function busyState(on, text) {
    busy = on;
    send.disabled = on;
    input.disabled = on;
    status.hidden = !on;
    if (on) status.textContent = text || "Working…";
  }

  try {
    const t = await getJSON(`/api/thread?repo=${encodeURIComponent(repo)}&pr=${encodeURIComponent(pr)}`);
    if (!t.messages.length) {
      bubble("system", "No message yet. The agent also reads the reply a previous run posted and the pull request diff, so a question about them does not need a summary.");
    }
    for (const m of t.messages) bubble(m.role, m.text, answerExtra(m));
  } catch (err) {
    bubble("system", `Cannot read the thread: ${err.message}`);
  }

  async function dispatch() {
    const text = input.value.trim();
    if (!text || busy) return;
    input.value = "";
    busyState(true, "Starting the run…");
    let started;
    try {
      started = await postJSON("/api/ask", { repo, pr, question: text });
    } catch (err) {
      busyState(false);
      input.value = text;
      bubble("system", `The run did not start: ${err.body?.error || err.message}`);
      return;
    }
    bubble("user", text);
    follow(started.execution_id);
  }

  function follow(id) {
    let terminal = false;
    stream = new EventSource(`/api/stream?id=${encodeURIComponent(id)}`);
    stream.onmessage = (ev) => {
      let e;
      try { e = JSON.parse(ev.data); } catch { return; }
      if (e.kind === "message.assistant" && e.text) {
        status.textContent = `Answering… ${String(e.text).slice(0, 120)}`;
        return;
      }
      if (e.step) status.textContent = `${e.step} · ${e.status || e.kind}`;
      if (e.kind === "workflow.completed" || e.kind === "workflow.failed" || e.kind === "workflow.cancelled") {
        terminal = true;
        finish(id, e);
      }
    };
    stream.onerror = () => {
      // The stream closes at the end of a run; ask the disk for the outcome.
      if (!terminal) finish(id, null);
    };
  }

  async function finish(id, last) {
    if (stream) { stream.close(); stream = null; }
    if (last && last.kind === "workflow.failed") {
      busyState(false);
      bubble("system", `The run failed${last.error ? `: ${last.error}` : ""}. Run ${id.slice(0, 8)}. Nothing was added to the thread.`);
      return;
    }
    busyState(true, "Reading the answer…");
    try {
      const msg = await postJSON("/api/ask/answer", { repo, pr, run: id });
      bubble("assistant", msg.text, answerExtra(msg));
    } catch (err) {
      const why = err.status === 404 ? "the run wrote no answer" : `the answer could not be read (${err.body?.error || err.message})`;
      bubble("system", `${why}. Run ${id.slice(0, 8)}; its log is in Activity.`);
    } finally {
      busyState(false);
      input.focus();
    }
  }

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" && !ev.shiftKey) {
      ev.preventDefault();
      dispatch();
    }
  });
  queueMicrotask(() => input.focus());
  return modal;
}
