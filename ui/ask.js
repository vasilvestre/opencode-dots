import { h, richText } from "./dom.js";
import { getJSON, postJSON } from "./api.js";
import { when } from "./format.js";
import { openModal } from "./modal.js";
import { openRunModal } from "./run.js";
import { toast } from "./toast.js";
import { t } from "./i18n.js";

// One conversation per pull request: the operator writes, the ask-pr workflow
// answers, and the page keeps the exchange on disk so a reload finds it again.
// The agent reads the same thread before answering, so it remembers the turns.
export async function openAskModal({ repo, pr, title }) {
  const log = h("div", { class: "chat-log", "aria-live": "polite" });
  const status = h("p", { class: "chat-status muted", hidden: true }, "");
  const input = h("textarea", {
    class: "chat-input",
    rows: 3,
    placeholder: t("ask.ask_about", [repo, pr]),
    "aria-label": t("ask.message_to_the_agent"),
  });
  const send = h("button", { class: "btn primary", type: "submit" }, t("ask.send"));
  const form = h("form", { class: "composer", onsubmit: (ev) => { ev.preventDefault(); dispatch(); } },
    input, send);
  const body = h("div", { class: "chat" },
    h("div", { class: "chat-head" },
      h("b", {}, `${repo}#${pr}`),
      h("span", { class: "muted" }, title || ""),
    ),
    log, status, form);

  const modal = openModal({ title: t("ask.ask", [repo, pr]), body, width: "760px" });

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
        h("span", { class: "muted" }, t("ask.next_step")),
        m.suggest.map((sg) => h("button", {
          class: "btn sm",
          type: "button",
          title: t("ask.start_for", [sg.workflow, repo, pr]),
          onclick: () => runSuggested(sg),
        }, t("common.run", [sg.workflow]))),
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
        toast(t("ask.awf_has_no_workflow_named", [sg.workflow]), { tone: "err" });
        return;
      }
      const detail = await getJSON(`/awf/api/workflows/${encodeURIComponent(wf.scope)}/${encodeURIComponent(wf.name)}`);
      openRunModal({
        scope: wf.scope,
        name: wf.name,
        inputs: detail.body?.Inputs || [],
        prefill: { repo, pr, ...(sg.inputs || {}) },
        note: t("ask.suggested_by_the_agent_from"),
      });
    } catch (err) {
      toast(t("ask.cannot_open", [sg.workflow, err.message]), { tone: "err" });
    }
  }

  function busyState(on, text) {
    busy = on;
    send.disabled = on;
    input.disabled = on;
    status.hidden = !on;
    if (on) status.textContent = text || t("ask.working");
  }

  try {
    const thread = await getJSON(`/api/thread?repo=${encodeURIComponent(repo)}&pr=${encodeURIComponent(pr)}`);
    if (!thread.messages.length) {
      bubble("system", t("ask.no_message_yet_the_agent"));
    }
    for (const m of thread.messages) bubble(m.role, m.text, answerExtra(m));
  } catch (err) {
    bubble("system", t("ask.cannot_read_the_thread", [err.message]));
  }

  async function dispatch() {
    const text = input.value.trim();
    if (!text || busy) return;
    input.value = "";
    busyState(true, t("ask.starting_the_run"));
    let started;
    try {
      started = await postJSON("/api/ask", { repo, pr, question: text });
    } catch (err) {
      busyState(false);
      input.value = text;
      bubble("system", t("ask.the_run_did_not_start", [err.body?.error || err.message]));
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
        status.textContent = t("ask.answering", [String(e.text).slice(0, 120)]);
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
      bubble("system", t("ask.the_run_failed_run_nothing", [last.error ? `: ${last.error}` : "", id.slice(0, 8)]));
      return;
    }
    busyState(true, t("ask.reading_the_answer"));
    try {
      const msg = await postJSON("/api/ask/answer", { repo, pr, run: id });
      bubble("assistant", msg.text, answerExtra(msg));
    } catch (err) {
      const why = err.status === 404 ? t("ask.the_run_wrote_no_answer") : t("ask.the_answer_could_not_be", [err.body?.error || err.message]);
      bubble("system", t("ask.run_its_log_is_in", [why, id.slice(0, 8)]));
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
