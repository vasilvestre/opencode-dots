import { h, mount } from "../dom.js";
import { getJSON, postJSON } from "../api.js";
import { ago, when } from "../format.js";
import { openModal } from "../modal.js";
import { toast } from "../toast.js";

// Link proposals (ADR 0041, first step): two work items a workflow thinks are one piece of
// work. Accept moves the references of the second onto the first and keeps the second as
// merged; Decline closes the proposal; Split undoes an accepted link and moves the references
// back. None writes to a tracker. The reason is text from
// outside, shown as text only.
export function mountLinks(el) {
  const out = h("div", { class: "stack" });
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, "Links"),
      h("p", { class: "muted" }, "Work items a workflow proposes to treat as one. Accepting moves references inside the database and nothing else; nothing is written to GitHub, Jira or Sentry.")),
    out,
  ));

  function item(it) {
    return h("td", { style: { minWidth: "11rem" } },
      h("div", {}, it.title || "(no title)"),
      h("div", { class: "muted mono" }, (it.refs || []).join(", ") || "no reference"),
      h("div", { class: "muted" }, `${it.repo} · ${it.workspace} · trust ${it.trust}`),
      it.state === "merged" ? h("span", { class: "badge" }, "merged") : null);
  }

  async function decide(kind, p, close) {
    try {
      const res = await postJSON(`/api/links/${kind}`, { id: p.id });
      close?.();
      toast(`Link ${res.state}`, { tone: "ok" });
      load();
    } catch (err) {
      close?.();
      const name = { accept: "Accept", decline: "Decline", split: "Split" }[kind];
      toast(`${name} refused: ${err.message}`, { tone: "err" });
    }
  }

  function confirmAccept(p) {
    openModal({
      title: "Accept this link?",
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, "Keeps"), h("dd", {}, `${p.a.title || p.a.id} (${p.a.refs.join(", ") || "no reference"})`),
          h("dt", {}, "Merged in"), h("dd", {}, `${p.b.title || p.b.id} (${p.b.refs.join(", ") || "no reference"})`)),
        h("p", { class: "muted" }, "The references of the second item move onto the first, and the second is kept as merged. Nothing is written to a tracker. The history keeps both.")),
      actions: [
        { label: "Cancel", onClick: (close) => close() },
        { label: "Accept", class: "primary", onClick: (close) => decide("accept", p, close) },
      ],
    });
  }

  function confirmSplit(p) {
    openModal({
      title: "Split this link?",
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, "Keeps"), h("dd", {}, `${p.a.title || p.a.id} (${p.a.refs.join(", ") || "no reference"})`),
          h("dt", {}, "Comes back"), h("dd", {}, `${p.b.title || p.b.id}`)),
        h("p", { class: "muted" }, "The references the accept moved go back to the second item, which is reopened in its earlier state. Nothing is written to a tracker. It is refused if one of those references moved again or the kept item is claimed.")),
      actions: [
        { label: "Cancel", onClick: (close) => close() },
        { label: "Split", class: "primary", onClick: (close) => decide("split", p, close) },
      ],
    });
  }

  function decided(p) {
    if (p.state === "split") {
      return h("td", { title: when(p.split_at) }, h("span", { class: "badge warn" }, "split"), ` ${ago(p.split_at)} by ${p.split_by}`);
    }
    return h("td", { title: when(p.decided_at) }, h("span", { class: p.state === "accepted" ? "badge ok" : "badge" }, p.state), ` ${ago(p.decided_at)} by ${p.decided_by}`);
  }

  function table(title, hint, rows, open) {
    const head = ["Keeps", "Merged in", "Confidence", "Reason", open ? "Proposed" : "Decided", ""];
    const body = rows.length
      ? h("div", { class: "table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, head.map((c) => h("th", {}, c)))),
        h("tbody", {}, rows.map((p) => h("tr", {},
          item(p.a),
          item(p.b),
          h("td", {}, `${Math.round(p.confidence * 100)}%`),
          h("td", { style: { minWidth: "10rem" } }, p.reason || "-"),
          open
            ? h("td", { title: when(p.created_at) }, `${ago(p.created_at)} by ${p.proposed_by}`)
            : decided(p),
          h("td", {}, open ? h("div", { class: "row" },
            p.boundary
              ? h("span", { class: "badge warn", title: "Different workspace or trust level: accepting is refused, a link never carries content across that boundary." }, "boundary")
              : h("button", { class: "btn sm", type: "button", onclick: () => confirmAccept(p) }, "Accept"),
            h("button", { class: "btn sm", type: "button", onclick: () => decide("decline", p) }, "Decline"))
            : p.state === "accepted" && p.b.state === "merged" && p.b.merged_into === p.a.id
              ? h("button", { class: "btn sm", type: "button", onclick: () => confirmSplit(p) }, "Split")
              : null),
        ))),
      ))
      : h("div", { class: "empty" }, "Nothing here.");
    return h("section", { class: "stack" }, h("h2", {}, title), h("p", { class: "muted" }, hint), body);
  }

  function load() {
    getJSON("/api/links")
      .then((list) => mount(out,
        table("Open", "A person decides. Nothing changes until Accept.", list.filter((p) => p.state === "open"), true),
        table("Decided", "Accepted, declined or split, newest first. An accepted link can be split.", list.filter((p) => p.state !== "open"), false)))
      .catch((err) => mount(out, h("div", { class: "empty" }, `Cannot read the link proposals (${err.message}).`)));
  }
  load();

  return { destroy() {} };
}
