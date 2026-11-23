import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { createEditor } from "../editor.js";
import { bytes, when } from "../format.js";
import { navigate } from "../router.js";

const KIND_LABEL = {
  watcher: "watcher rules: which repository, which workflow, which events",
  list: "repositories visited by the daily pass, one per line",
  map: "host paths for the workflow shell steps",
};

// Local configs are the files the agent stack reads from the repository root.
// They never leave this machine; the page still edits them like the workflows.
export function mountConfig(el, arg) {
  let destroyed = false;
  let editor = null;
  const listEl = h("div", {});

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, "Config"),
        h("p", { class: "muted" }, "Files awf-gh and the workflows read at run time. Saves keep a stamped backup beside the file."),
      ),
      arg ? h("button", { class: "btn", onclick: () => navigate("config") }, "All files") : null,
    ),
    listEl,
    h("div", { id: "config-editor" }),
  ));

  async function load() {
    let files;
    try {
      files = (await getJSON("/api/files")).filter((f) => f.kind !== "workflow");
    } catch (err) {
      mount(listEl, h("div", { class: "empty" }, `Cannot list files (${err.message}).`));
      return;
    }
    const selected = files.find((f) => f.path === arg);
    if (selected) {
      showEditor(selected);
      mount(listEl, h("div", { class: "chips" }, files.map((f) =>
        h("a", {
          class: "chip",
          href: `#config/${f.path}`,
          "aria-current": f.path === selected.path ? "true" : null,
        }, f.path))));
      return;
    }
    editor?.destroy();
    editor = null;
    el.querySelector("#config-editor").replaceChildren();
    mount(listEl, h("div", { class: "kpis" }, files.map((f) => h("a", {
      class: "card",
      href: `#config/${f.path}`,
      style: { textDecoration: "none", color: "inherit" },
    },
    h("div", { class: "row between" },
      h("b", { class: "mono" }, f.path),
      f.exists ? h("span", { class: "badge ok" }, "on disk") : h("span", { class: "badge warn" }, "missing"),
    ),
    h("p", { class: "muted", style: { marginTop: "6px", fontSize: "12px" } }, KIND_LABEL[f.kind] || f.kind),
    h("div", { class: "file-meta", style: { marginTop: "10px" } },
      f.exists ? h("span", {}, bytes(f.size)) : null,
      f.exists ? h("span", {}, `edited ${when(f.mod_time)}`) : null,
      h("span", {}, "not in git"),
    ),
    ))));
  }

  async function showEditor(file) {
    editor?.destroy();
    editor = await createEditor({ path: file.path, kind: file.kind });
    if (destroyed) { editor.destroy(); return; }
    if (arg !== file.path) return;
    mount(el.querySelector("#config-editor"), h("div", { class: "card" },
      h("div", { class: "row between", style: { marginBottom: "10px" } },
        h("h2", {}, file.path),
        h("span", { class: "muted", style: { fontSize: "12px" } }, KIND_LABEL[file.kind] || file.kind),
      ),
      editor.el,
    ));
  }

  load();

  return {
    destroy() {
      destroyed = true;
      editor?.destroy();
      editor = null;
    },
  };
}
