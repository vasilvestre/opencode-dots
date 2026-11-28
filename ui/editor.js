import { h, mount } from "./dom.js";
import { awf, getText, putJSON } from "./api.js";
import { toast } from "./toast.js";
import { openRunModal } from "./run.js";
import { navigate } from "./router.js";
import { t } from "./i18n.js";

// Shared source editor for workflows and local configs. It writes files
// through /api/save and never touches git: the repository is the undo.
// Validation is awf's own endpoint, which re-reads the file from disk.
export async function createEditor({ path, kind, scope = "global", name, onSave, onCursor, readOnly = false }) {
  const state = { saved: "", busy: false, dirty: false };

  const gutter = h("div", { class: "gutter", "aria-hidden": "true" });
  const area = h("textarea", {
    class: "code-area",
    spellcheck: false,
    autocapitalize: "off",
    autocomplete: "off",
    wrap: "off",
    "aria-label": t("editor.content_of", [path]),
    readonly: readOnly ? "" : null,
  });
  const dirtyDot = h("span", { class: "dirty-dot", hidden: true, title: t("common.unsaved_changes") });
  const pathLabel = h("span", { class: "path" }, path);
  const issues = h("div", { class: "issues", hidden: true });
  const saveBtn = h("button", { class: "btn primary", onclick: () => save(), title: t("editor.save_ctrl_s") }, t("common.save"));
  const revertBtn = h("button", { class: "btn", onclick: () => revert(), title: t("editor.reload_from_disk") }, t("editor.revert"));
  const validateBtn = kind === "workflow"
    ? h("button", { class: "btn", onclick: () => validate(), title: t("editor.save_then_ask_awf_to") }, t("editor.validate"))
    : null;
  const runBtn = kind === "workflow"
    ? h("button", { class: "btn", onclick: () => run(), title: t("editor.run_with_inputs") }, t("editor.run"))
    : null;

  const el = h("div", { class: "editor" },
    h("div", { class: "editor-toolbar" },
      pathLabel,
      dirtyDot,
      h("span", { class: "spacer" }),
      readOnly ? null : revertBtn,
      validateBtn,
      readOnly ? null : saveBtn,
      runBtn,
    ),
    issues,
    h("div", { class: "code-box" }, gutter, area),
    h("div", { class: "file-meta" },
      readOnly ? h("span", {}, t("editor.read_only")) : h("span", { class: "mono" }, t("editor.ctrl_s_save")),
      readOnly ? null : h("span", {}, t("editor.tab_inserts_two_spaces")),
      validateBtn ? h("span", {}, t("editor.validate_reads_the_file_from")) : null,
    ),
  );

  if (readOnly) {
    area.readOnly = true;
    saveBtn.hidden = true;
    revertBtn.hidden = true;
  }

  const beforeUnload = (ev) => {
    if (!state.dirty) return;
    ev.preventDefault();
    ev.returnValue = "";
  };
  window.addEventListener("beforeunload", beforeUnload);

  function paintGutter() {
    const lines = area.value.split("\n").length;
    gutter.textContent = Array.from({ length: lines }, (_, i) => i + 1).join("\n");
    gutter.scrollTop = area.scrollTop;
  }

  function setDirty() {
    state.dirty = area.value !== state.saved;
    dirtyDot.hidden = !state.dirty;
    saveBtn.disabled = !state.dirty || state.busy;
    revertBtn.disabled = state.busy;
    if (!state.dirty && issues.classList.contains("ok")) setIssues(null);
  }

  function setBusy(busy) {
    state.busy = busy;
    saveBtn.disabled = busy || !state.dirty;
    if (validateBtn) validateBtn.disabled = busy;
    if (runBtn) runBtn.disabled = busy;
  }

  function setIssues(tone, title, lines = []) {
    if (!tone) { issues.hidden = true; return; }
    issues.hidden = false;
    issues.className = `issues ${tone}`;
    mount(issues,
      h("div", { class: "head" }, title),
      lines.length ? h("ul", {}, lines.map((l) => h("li", {}, String(l)))) : null,
    );
  }

  function load(text) {
    state.saved = text;
    area.value = text;
    paintGutter();
    setDirty();
  }

  async function revert() {
    try {
      load(await getText(`/api/source?path=${encodeURIComponent(path)}`));
      setIssues(null);
      toast(t("editor.reloaded_from_disk", [path]), { tone: "info" });
    } catch (err) {
      setIssues("bad", t("editor.cannot_read_the_file"), [err.message]);
    }
  }

  async function save({ quiet = false } = {}) {
    if (state.busy) return false;
    setBusy(true);
    try {
      const res = await putJSON("/api/save", { path, content: area.value });
      state.saved = area.value;
      setIssues("ok", t("editor.saved_to_disk"),
        [res.backup ? t("editor.previous_content_kept_as", [res.backup]) : t("editor.new_file")]);
      if (!quiet) toast(t("editor.saved", [path]), { tone: "ok" });
      Promise.resolve().then(() => onSave?.()).catch(() => {});
      return true;
    } catch (err) {
      setIssues("bad", err.status === 422 ? t("editor.yaml_does_not_parse_nothing") : t("editor.save_failed"),
        [err.message]);
      toast(err.message, { tone: "err", timeout: 8000 });
      return false;
    } finally {
      setBusy(false);
      setDirty();
    }
  }

  async function validate() {
    if (state.dirty && !(await save({ quiet: true }))) return;
    setBusy(true);
    try {
      const res = await awf(`/api/workflows/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/validate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
        timeout: 30000,
      });
      const errors = res?.body?.errors || res?.errors || [];
      if (errors.length) setIssues("bad", t("editor.awf_reports_error_s", [errors.length]), errors);
      else setIssues("ok", t("editor.awf_accepts_this_workflow"), [t("editor.it_can_be_run_from")]);
    } catch (err) {
      const detail = err.body?.detail || err.body?.error || err.message;
      const errors = Array.isArray(detail) ? detail : [detail];
      if (!Array.isArray(detail)) errors.push(t("editor.awf_stops_at_the_first"));
      setIssues("bad", t("editor.awf_refuses_this_workflow"), errors);
    } finally {
      setBusy(false);
    }
  }

  async function run() {
    if (state.dirty && !(await save({ quiet: true }))) return;
    let workflow;
    try {
      workflow = (await awf(`/api/workflows/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`)).body;
    } catch (err) {
      toast(t("editor.cannot_load_the_workflow", [err.message]), { tone: "err" });
      return;
    }
    openRunModal({ scope, name, inputs: workflow.Inputs || [], onStarted: (id) => {
      toast(t("common.run_started", [id.slice(0, 8)]), { tone: "ok" });
      navigate("activity");
    } });
  }

  const cursorLine = () => area.value.slice(0, area.selectionStart).split("\n").length;
  let lastLine = 0;
  const reportCursor = () => {
    const line = cursorLine();
    if (line !== lastLine) { lastLine = line; onCursor?.(line, area.value); }
  };
  area.addEventListener("keyup", reportCursor);
  area.addEventListener("click", reportCursor);
  area.addEventListener("input", () => { paintGutter(); setDirty(); });
  area.addEventListener("scroll", () => { gutter.scrollTop = area.scrollTop; });
  area.addEventListener("keydown", (ev) => {
    if (readOnly) return;
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "s") {
      ev.preventDefault();
      save();
    } else if (ev.key === "Tab" && !ev.ctrlKey && !ev.metaKey) {
      ev.preventDefault();
      const { selectionStart: a, selectionEnd: b, value } = area;
      area.value = `${value.slice(0, a)}  ${value.slice(b)}`;
      area.selectionStart = area.selectionEnd = a + 2;
      paintGutter();
      setDirty();
    }
  });

  try {
    load(await getText(`/api/source?path=${encodeURIComponent(path)}`));
  } catch (err) {
    setIssues("bad", t("editor.cannot_read_the_file"), [err.message]);
    area.disabled = true;
  }

  return {
    el,
    isDirty: () => state.dirty,
    text: () => area.value,
    // Select a 1-based line and scroll it into the upper third of the box.
    gotoLine(line) {
      const lines = area.value.split("\n");
      const idx = Math.max(0, Math.min(lines.length - 1, line - 1));
      const start = lines.slice(0, idx).reduce((n, l) => n + l.length + 1, 0);
      lastLine = idx + 1;
      area.focus({ preventScroll: true });
      area.setSelectionRange(start, start + lines[idx].length);
      const lh = parseFloat(getComputedStyle(area).lineHeight) || 19;
      area.scrollTop = Math.max(0, idx * lh - area.clientHeight / 3);
      gutter.scrollTop = area.scrollTop;
    },
    destroy() {
      window.removeEventListener("beforeunload", beforeUnload);
    },
    reload: revert,
  };
}
