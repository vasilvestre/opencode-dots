import { h, mount } from "./dom.js";
import { awf, getText, putJSON } from "./api.js";
import { toast } from "./toast.js";
import { openRunModal } from "./run.js";
import { navigate } from "./router.js";

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
    "aria-label": `Content of ${path}`,
    readonly: readOnly ? "" : null,
  });
  const dirtyDot = h("span", { class: "dirty-dot", hidden: true, title: "Unsaved changes" });
  const pathLabel = h("span", { class: "path" }, path);
  const issues = h("div", { class: "issues", hidden: true });
  const saveBtn = h("button", { class: "btn primary", onclick: () => save(), title: "Save (Ctrl+S)" }, "Save");
  const revertBtn = h("button", { class: "btn", onclick: () => revert(), title: "Reload from disk" }, "Revert");
  const validateBtn = kind === "workflow"
    ? h("button", { class: "btn", onclick: () => validate(), title: "Save, then ask awf to validate" }, "Validate")
    : null;
  const runBtn = kind === "workflow"
    ? h("button", { class: "btn", onclick: () => run(), title: "Run with inputs" }, "Run")
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
      readOnly ? h("span", {}, "read only") : h("span", { class: "mono" }, "ctrl+s save"),
      readOnly ? null : h("span", {}, "tab inserts two spaces"),
      validateBtn ? h("span", {}, "validate reads the file from disk, so it saves first") : null,
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
      toast(`Reloaded ${path} from disk`, { tone: "info" });
    } catch (err) {
      setIssues("bad", "Cannot read the file", [err.message]);
    }
  }

  async function save({ quiet = false } = {}) {
    if (state.busy) return false;
    setBusy(true);
    try {
      const res = await putJSON("/api/save", { path, content: area.value });
      state.saved = area.value;
      setIssues("ok", "Saved to disk",
        [res.backup ? `Previous content kept as ${res.backup}.` : "New file."]);
      if (!quiet) toast(`Saved ${path}`, { tone: "ok" });
      Promise.resolve().then(() => onSave?.()).catch(() => {});
      return true;
    } catch (err) {
      setIssues("bad", err.status === 422 ? "YAML does not parse, nothing written" : "Save failed",
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
      if (errors.length) setIssues("bad", `awf reports ${errors.length} error(s)`, errors);
      else setIssues("ok", "awf accepts this workflow", ["It can be run from the Run button."]);
    } catch (err) {
      const detail = err.body?.detail || err.body?.error || err.message;
      const errors = Array.isArray(detail) ? detail : [detail];
      if (!Array.isArray(detail)) errors.push("awf stops at the first structural error and does not name the line. Check states.initial and every OnSuccess target.");
      setIssues("bad", "awf refuses this workflow", errors);
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
      toast(`Cannot load the workflow: ${err.message}`, { tone: "err" });
      return;
    }
    openRunModal({ scope, name, inputs: workflow.Inputs || [], onStarted: (id) => {
      toast(`Run started: ${id.slice(0, 8)}`, { tone: "ok" });
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
    setIssues("bad", "Cannot read the file", [err.message]);
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
