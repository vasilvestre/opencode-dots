import { h, mount } from "../dom.js";
import { getJSON, postJSON } from "../api.js";
import { ago, when } from "../format.js";
import { openModal } from "../modal.js";
import { toast } from "../toast.js";
import { failed, skeleton } from "../state.js";
import { t } from "../i18n.js";

// Link proposals (ADR 0041, first step): two work items a workflow thinks are one piece of
// work. Accept moves the references of the second onto the first and keeps the second as
// merged; Decline closes the proposal; Split undoes an accepted link and moves the references
// back. None writes to a tracker. The reason is text from
// outside, shown as text only.
export function mountLinks(el) {
  const out = h("div", { class: "stack" }, skeleton());
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, t("common.links")),
      h("p", { class: "muted" }, t("links.work_items_a_workflow_proposes"))),
    out,
  ));

  function item(it) {
    return h("td", { style: { minWidth: "11rem" } },
      h("div", {}, it.title || t("links.no_title")),
      h("div", { class: "muted mono" }, (it.refs || []).join(", ") || t("links.no_reference")),
      h("div", { class: "muted" }, t("links.trust", [it.repo, it.workspace, it.trust])),
      it.state === "merged" ? h("span", { class: "badge" }, "merged") : null);
  }

  async function decide(kind, p, close) {
    try {
      const res = await postJSON(`/api/links/${kind}`, { id: p.id });
      close?.();
      toast(t("links.link", [res.state]), { tone: "ok" });
      load();
    } catch (err) {
      close?.();
      const name = { accept: t("links.accept"), decline: t("links.decline"), split: t("links.split") }[kind];
      toast(t("links.refused", [name, err.message]), { tone: "err" });
    }
  }

  function confirmAccept(p) {
    openModal({
      title: t("links.accept_this_link"),
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, t("links.keeps")), h("dd", {}, `${p.a.title || p.a.id} (${p.a.refs.join(", ") || "no reference"})`),
          h("dt", {}, t("links.merged_in")), h("dd", {}, `${p.b.title || p.b.id} (${p.b.refs.join(", ") || "no reference"})`)),
        h("p", { class: "muted" }, t("links.the_references_of_the_second"))),
      actions: [
        { label: t("common.cancel"), onClick: (close) => close() },
        { label: t("links.accept"), class: "primary", onClick: (close) => decide("accept", p, close) },
      ],
    });
  }

  function confirmSplit(p) {
    openModal({
      title: t("links.split_this_link"),
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, t("links.keeps")), h("dd", {}, `${p.a.title || p.a.id} (${p.a.refs.join(", ") || "no reference"})`),
          h("dt", {}, t("links.comes_back")), h("dd", {}, `${p.b.title || p.b.id}`)),
        h("p", { class: "muted" }, t("links.the_references_the_accept_moved"))),
      actions: [
        { label: t("common.cancel"), onClick: (close) => close() },
        { label: t("links.split"), class: "primary", onClick: (close) => decide("split", p, close) },
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
    const head = [t("links.keeps"), t("links.merged_in"), t("links.confidence"), t("common.reason"), open ? t("links.proposed") : t("links.decided"), ""];
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
              ? h("span", { class: "badge warn", title: t("links.different_workspace_or_trust_level") }, "boundary")
              : h("button", { class: "btn sm", type: "button", onclick: () => confirmAccept(p) }, t("links.accept")),
            h("button", { class: "btn sm", type: "button", onclick: () => decide("decline", p) }, t("links.decline")))
            : p.state === "accepted" && p.b.state === "merged" && p.b.merged_into === p.a.id
              ? h("button", { class: "btn sm", type: "button", onclick: () => confirmSplit(p) }, t("links.split"))
              : null),
        ))),
      ))
      : h("div", { class: "empty" }, t("common.nothing_here"));
    return h("section", { class: "stack" }, h("h2", {}, title), h("p", { class: "muted" }, hint), body);
  }

  function load() {
    getJSON("/api/links")
      .then((list) => mount(out,
        table(t("links.open"), t("links.a_person_decides_nothing_changes"), list.filter((p) => p.state === "open"), true),
        table(t("links.decided"), t("links.accepted_declined_or_split_newest"), list.filter((p) => p.state !== "open"), false)))
      .catch((err) => mount(out, failed(t("links.cannot_read_the_link_proposals", [err.message]), load)));
  }
  load();

  return { destroy() {} };
}
