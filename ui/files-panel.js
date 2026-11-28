import { h, mount } from "./dom.js";
import { getJSON } from "./api.js";
import { createEditor } from "./editor.js";
import { bytes, when } from "./format.js";
import { confirmDialog } from "./modal.js";
import { setLeaveGuard } from "./router.js";
import { t } from "./i18n.js";

// The raw files behind the Repos view: repos.yaml (editable) and what is generated
// from it (read-only). Loaded on first open, so the page costs nothing until asked.
export function filesPanel(initial) {
  let destroyed = false;
  let editor = null;
  let files = [];
  let current = "";
  let loaded = false;
  const tabs = h("div", { class: "row", style: { flexWrap: "wrap", gap: "6px" } });
  const body = h("div", {});
  const el = h("div", { class: "stack", style: { marginTop: "10px" } }, tabs, body);

  const dirty = () => Boolean(editor?.isDirty());
  const ok = () => !dirty() || confirmDialog({
    title: t("common.discard_unsaved_changes"),
    message: t("config.this_section_has_edits_that"),
    confirm: t("common.discard"),
    danger: true,
  });
  const beforeUnload = (ev) => { if (dirty()) { ev.preventDefault(); ev.returnValue = ""; } };
  window.addEventListener("beforeunload", beforeUnload);
  setLeaveGuard(ok);

  function paintTabs() {
    mount(tabs, files.map((f) => h("button", {
      class: `btn sm${f.path === current ? " primary" : f.generated ? " ghost" : ""}`,
      title: f.generated ? t("config.generated_read_only") : "",
      onclick: () => select(f.path),
    }, f.path, f.path === current && dirty() ? h("span", { class: "dirty-dot", style: { marginLeft: "6px" } }) : null)));
  }

  async function select(path) {
    if (path === current || !(await ok())) return;
    const file = files.find((f) => f.path === path);
    if (!file) { mount(body, h("div", { class: "empty" }, t("config.no_local_config_named", [path]))); return; }
    current = path;
    editor?.destroy();
    editor = await createEditor({ path, kind: file.kind, readOnly: Boolean(file.generated) });
    if (destroyed) { editor.destroy(); return; }
    mount(body, h("div", { class: "card" },
      h("div", { class: "row between", style: { marginBottom: "10px" } },
        h("h2", {}, path),
        h("span", { class: "muted", style: { fontSize: "12px" } },
          file.exists ? t("config.edited_not_in_git", [bytes(file.size), when(file.mod_time)])
            : file.optional ? t("config.not_generated_no_repository_needs") : t("config.missing_on_disk_saving_creates"))),
      file.generated ? h("p", { class: "warning", style: { marginBottom: "10px" } }, t("config.this_file_is_written_from")) : null,
      editor.el));
    paintTabs();
    editor.el.addEventListener("input", paintTabs);
  }

  async function load() {
    if (loaded) return;
    loaded = true;
    try {
      files = (await getJSON("/api/files")).filter((f) => f.kind !== "workflow");
    } catch (err) {
      mount(body, h("div", { class: "empty" }, t("config.cannot_list_files", [err.message])));
      return;
    }
    if (destroyed) return;
    // repos.yaml first: it is the one to edit, the others only to read.
    files.sort((a, b) => Number(Boolean(a.generated)) - Number(Boolean(b.generated)));
    paintTabs();
    await select(initial || files.find((f) => !f.generated)?.path || files[0]?.path);
  }

  return {
    el,
    load,
    destroy() {
      destroyed = true;
      window.removeEventListener("beforeunload", beforeUnload);
      setLeaveGuard(null);
      editor?.destroy();
    },
  };
}
