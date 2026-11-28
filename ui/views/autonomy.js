import { h, mount } from "../dom.js";
import { getJSON, postJSON, request } from "../api.js";
import { ago, when } from "../format.js";
import { openModal } from "../modal.js";
import { toast } from "../toast.js";
import { failed, skeleton } from "../state.js";
import { t } from "../i18n.js";

// What the autonomy ceiling stopped: notes (observe, class 3) and held items
// (propose). A Release click asks the dashboard to start the publish-held run
// through awf; the dashboard itself publishes nothing.
export function mountAutonomy(el) {
  const out = h("div", { class: "stack" }, skeleton());
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, t("common.autonomy")),
      h("p", { class: "muted" }, t("autonomy.what_the_ceiling_kept_from"))),
    out,
  ));

  async function readArtifact(item) {
    try {
      const text = await request(`/api/autonomy/artifact?kind=${encodeURIComponent(item.kind)}&name=${encodeURIComponent(item.name)}`);
      openModal({ title: item.artifact, body: h("pre", {}, typeof text === "string" ? text : JSON.stringify(text, null, 2)) });
    } catch (err) {
      openModal({ title: item.artifact, body: h("div", { class: "empty" }, t("autonomy.cannot_read_it", [err.message])) });
    }
  }

  function confirmRelease(item) {
    openModal({
      title: t("autonomy.release_this_held_item"),
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, t("autonomy.action")), h("dd", {}, item.action || "?"),
          h("dt", {}, t("autonomy.ref")), h("dd", {}, item.ref || "unknown"),
          ...(item.target ? [h("dt", {}, t("common.target")), h("dd", {}, item.target)] : []),
          ...(item.title ? [h("dt", {}, t("autonomy.title")), h("dd", {}, item.title)] : []),
          h("dt", {}, t("autonomy.artifact")), h("dd", { class: "mono" }, item.artifact || "none")),
        h("p", { class: "muted" }, t("autonomy.the_publish_held_workflow_will"))),
      actions: [
        { label: t("common.cancel"), onClick: (close) => close() },
        {
          label: t("autonomy.release"), class: "primary",
          onClick: async (close) => {
            try {
              const res = await postJSON("/api/autonomy/release", { id: item.name });
              close();
              toast(t("autonomy.publish_held_started", [res.execution_id]), { tone: "ok" });
            } catch (err) {
              toast(t("autonomy.release_refused", [err.message]), { tone: "err" });
            }
          },
        },
      ],
    });
  }

  function table(title, hint, items, { release = false, released = false } = {}) {
    const body = items.length
      ? h("div", { class: "table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, [t("autonomy.action"), t("autonomy.ref"), t("autonomy.class"), t("common.reason"), t("autonomy.age"), t("autonomy.artifact"), ...(release ? [""] : [])].map((c) => h("th", {}, c)))),
        h("tbody", {}, items.map((it) => h("tr", {},
          h("td", {}, h("span", { class: "badge" }, it.action || "?"), released ? h("span", { class: "badge ok" }, "released") : null),
          h("td", it.target || it.title ? { style: { minWidth: "11rem" } } : {}, it.ref || "unknown",
            it.target ? h("div", { class: "muted", title: t("autonomy.file_issue_target") }, `to ${it.target}`) : null,
            it.title ? h("div", { class: "muted", title: it.title }, it.title) : null),
          h("td", {}, it.class && it.class !== "none" ? it.class : "-"),
          h("td", {}, it.reason),
          h("td", { title: when(it.at) }, ago(it.at)),
          h("td", {}, it.artifact
            ? (it.readable
              ? h("a", { href: "#autonomy", title: it.artifact, onclick: (ev) => { ev.preventDefault(); readArtifact(it); } }, "read")
              : h("span", { class: "muted", title: it.artifact }, "unavailable"))
            : h("span", { class: "muted" }, "none")),
          release ? h("td", {}, it.artifact && it.readable
            ? h("button", { class: "btn sm", type: "button", onclick: () => confirmRelease(it) }, t("autonomy.release"))
            : h("span", { class: "muted", title: t("autonomy.needs_a_readable_artifact") }, "-")) : null,
        ))),
      ))
      : h("div", { class: "empty" }, t("common.nothing_here"));
    return h("section", { class: "stack" }, h("h2", {}, title), h("p", { class: "muted" }, hint), body);
  }

  getJSON("/api/autonomy")
    .then((data) => {
      const tabs = [
        ["held", t("common.held"), () => table(t("common.held"), t("autonomy.propose_a_person_decides_whether"), data.held || [], { release: true }), (data.held || []).length],
        ["notes", t("common.notes"), () => table(t("common.notes"), t("autonomy.observe_or_a_class_diagnosis"), data.notes || []), (data.notes || []).length],
        ["released", t("autonomy.released"), () => table(t("autonomy.released"), t("autonomy.held_items_a_person_released"), data.released || [], { released: true }), (data.released || []).length],
      ];
      let active = "held";
      const paint = () => mount(out,
        h("div", { class: "seg", role: "tablist", "aria-label": t("autonomy.autonomy_lists") }, tabs.map(([key, label, , n]) =>
          h("button", { role: "tab", "aria-selected": String(active === key), onclick: () => { active = key; paint(); } }, `${label} (${n})`))),
        tabs.find(([key]) => key === active)[2]());
      paint();
    })
    .catch((err) => mount(out, failed(t("autonomy.cannot_read_the_autonomy_files", [err.message]), () => mountAutonomy(el))));

  return { destroy() {} };
}
