import { h, mount } from "../dom.js";
import { getJSON, postJSON, request } from "../api.js";
import { ago, when } from "../format.js";
import { openModal } from "../modal.js";
import { toast } from "../toast.js";

// What the autonomy ceiling stopped: notes (observe, class 3) and held items
// (propose). A Release click asks the dashboard to start the publish-held run
// through awf; the dashboard itself publishes nothing.
export function mountAutonomy(el) {
  const out = h("div", { class: "stack" });
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, "Autonomy"),
      h("p", { class: "muted" }, "What the ceiling kept from GitHub. Release starts the publish-held workflow after you confirm; this page publishes nothing itself.")),
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

  function confirmRelease(item) {
    openModal({
      title: "Release this held item?",
      body: h("div", { class: "stack" },
        h("dl", {},
          h("dt", {}, "Action"), h("dd", {}, item.action || "?"),
          h("dt", {}, "Ref"), h("dd", {}, item.ref || "unknown"),
          h("dt", {}, "Artifact"), h("dd", { class: "mono" }, item.artifact || "none")),
        h("p", { class: "muted" }, "The publish-held workflow will publish exactly this, once. It cannot be undone from here.")),
      actions: [
        { label: "Cancel", onClick: (close) => close() },
        {
          label: "Release", class: "primary",
          onClick: async (close) => {
            try {
              const res = await postJSON("/api/autonomy/release", { id: item.name });
              close();
              toast(`publish-held started (${res.execution_id})`, { tone: "ok" });
            } catch (err) {
              toast(`Release refused: ${err.message}`, { tone: "err" });
            }
          },
        },
      ],
    });
  }

  function table(title, hint, items, { release = false, released = false } = {}) {
    const body = items.length
      ? h("div", { class: "table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, ["Action", "Ref", "Class", "Reason", "Age", "Artifact", ...(release ? [""] : [])].map((c) => h("th", {}, c)))),
        h("tbody", {}, items.map((it) => h("tr", {},
          h("td", {}, h("span", { class: "badge" }, it.action || "?"), released ? h("span", { class: "badge ok" }, "released") : null),
          h("td", {}, it.ref || "unknown"),
          h("td", {}, it.class && it.class !== "none" ? it.class : "-"),
          h("td", {}, it.reason),
          h("td", { title: when(it.at) }, ago(it.at)),
          h("td", {}, it.artifact
            ? (it.readable
              ? h("a", { href: "#autonomy", title: it.artifact, onclick: (ev) => { ev.preventDefault(); readArtifact(it); } }, "read")
              : h("span", { class: "muted", title: it.artifact }, "unavailable"))
            : h("span", { class: "muted" }, "none")),
          release ? h("td", {}, it.artifact && it.readable
            ? h("button", { class: "btn sm", type: "button", onclick: () => confirmRelease(it) }, "Release")
            : h("span", { class: "muted", title: "needs a readable artifact" }, "-")) : null,
        ))),
      ))
      : h("div", { class: "empty" }, "Nothing here.");
    return h("section", { class: "stack" }, h("h2", {}, title), h("p", { class: "muted" }, hint), body);
  }

  getJSON("/api/autonomy")
    .then((data) => mount(out,
      table("Notes", "Observe, or a class 3 diagnosis: kept as an internal note.", data.notes || []),
      table("Held", "Propose: a person decides whether it goes out.", data.held || [], { release: true }),
      table("Released", "Held items a person released; the record moved to held/released.", data.released || [], { released: true })))
    .catch((err) => mount(out, h("div", { class: "empty" }, `Cannot read the autonomy files (${err.message}).`)));

  return { destroy() {} };
}
