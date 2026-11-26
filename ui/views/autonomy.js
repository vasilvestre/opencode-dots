import { h, mount } from "../dom.js";
import { getJSON, request } from "../api.js";
import { ago, when } from "../format.js";
import { openModal } from "../modal.js";

// What the autonomy ceiling stopped: notes (observe, class 3) and held items
// (propose). Read only. Releasing a held item is a later workflow.
export function mountAutonomy(el) {
  const out = h("div", { class: "stack" });
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, "Autonomy"),
      h("p", { class: "muted" }, "What the ceiling kept from GitHub. Nothing here posts, pushes or releases.")),
    out,
  ));

  async function readArtifact(item) {
    try {
      const text = await request(`/api/autonomy/artifact?kind=${encodeURIComponent(item.kind)}&name=${encodeURIComponent(item.name)}`);
      openModal({ title: item.artifact, body: h("pre", {}, typeof text === "string" ? text : JSON.stringify(text, null, 2)) });
    } catch (err) {
      openModal({ title: item.artifact, body: h("div", { class: "empty" }, `Cannot read it (${err.message}).`) });
    }
  }

  function table(title, hint, items) {
    const body = items.length
      ? h("div", { class: "table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Action", "Ref", "Class", "Reason", "Age", "Artifact"].map((c) => h("th", {}, c)))),
        h("tbody", {}, items.map((it) => h("tr", {},
          h("td", {}, h("span", { class: "badge" }, it.action || "?")),
          h("td", {}, it.ref || "unknown"),
          h("td", {}, it.class && it.class !== "none" ? it.class : "-"),
          h("td", {}, it.reason),
          h("td", { title: when(it.at) }, ago(it.at)),
          h("td", {}, it.artifact
            ? (it.readable
              ? h("a", { href: "#autonomy", title: it.artifact, onclick: (ev) => { ev.preventDefault(); readArtifact(it); } }, "read")
              : h("span", { class: "muted", title: it.artifact }, "unavailable"))
            : h("span", { class: "muted" }, "none")),
        ))),
      ))
      : h("div", { class: "empty" }, "Nothing here.");
    return h("section", { class: "stack" }, h("h2", {}, title), h("p", { class: "muted" }, hint), body);
  }

  getJSON("/api/autonomy")
    .then((data) => mount(out,
      table("Notes", "Observe, or a class 3 diagnosis: kept as an internal note.", data.notes || []),
      table("Held", "Propose: a person decides whether it goes out.", data.held || [])))
    .catch((err) => mount(out, h("div", { class: "empty" }, `Cannot read the autonomy files (${err.message}).`)));

  return { destroy() {} };
}
