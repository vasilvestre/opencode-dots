import { h, mount } from "./dom.js";
import { awf, getJSON, getText, putJSON } from "./api.js";
import { toast } from "./toast.js";
import { openModal } from "./modal.js";
import { navigate } from "./router.js";

// Shared source editor for workflows and local configs. It writes files
// through /api/save and never touches git: the repository is the undo.
// Validation is awf's own endpoint, which re-reads the file from disk.
export async function createEditor({ path, kind, scope = "global", name }) {
  const state = { saved: "", busy: false, dirty: false };
  let destroyed = false;

  const gutter = h("div", { class: "gutter", "aria-hidden": "true" });
  const area = h("textarea", {
    class: "code-area",
    spellcheck: false,
    autocapitalize: "off",
    autocomplete: "off",
    wrap: "off",
    "aria-label": `Content of ${path}`,
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
      revertBtn,
      validateBtn,
      saveBtn,
      runBtn,
    ),
    issues,
    h("div", { class: "code-box" }, gutter, area),
    h("div", { class: "file-meta" },
      h("span", { class: "mono" }, "ctrl+s save"),
      h("span", {}, "tab inserts two spaces"),
      validateBtn ? h("span", {}, "validate reads the file from disk, so it saves first") : null,
    ),
  );

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

  area.addEventListener("input", () => { paintGutter(); setDirty(); });
  area.addEventListener("scroll", () => { gutter.scrollTop = area.scrollTop; });
  area.addEventListener("keydown", (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "s") {
      ev.preventDefault();
      save();
    } else if (ev.key === "Tab" && !ev.ctrlKey && !ev.metaKey) {
      ev.preventDefault();
      const { selectionStart: a, selectionEnd: b, value } = area;
      area.value = value.slice(0, a) + "  " + value.slice(b);
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
    destroy() {
      destroyed = true;
      window.removeEventListener("beforeunload", beforeUnload);
    },
    reload: revert,
  };
}

function openRunModal({ scope, name, inputs, onStarted }) {
  let closeFn = () => {};
  const repoList = h("datalist", { id: "run-repos" });
  const isRepo = (input) => /^repos?(itory)?$/i.test(input.Name);
  const wantsRepos = inputs.some((input) => isRepo(input));

  const fields = inputs.map((input) => {
    const id = `run-${input.Name}`;
    const type = String(input.Type || "string").toLowerCase();
    const rules = input.Validation || {};
    const options = type === "bool" || type === "boolean"
      ? [["true", "true"], ["false", "false"]]
      : Array.isArray(rules.Enum) && rules.Enum.length
        ? rules.Enum.map((v) => [v, v])
        : null;
    let control;
    if (options) {
      control = h("select", { id },
        input.Required ? null : h("option", { value: "", selected: input.Default == null }, "not set"),
        options.map(([value, label]) => h("option", {
          value,
          selected: input.Default != null && String(input.Default) === value,
        }, label)),
      );
    } else if (type === "integer" || type === "number") {
      control = h("input", {
        id,
        type: "number",
        step: "1",
        value: input.Default ?? "",
        min: rules.Min ?? null,
        max: rules.Max ?? null,
      });
    } else {
      control = h("input", {
        id,
        type: "text",
        value: input.Default ?? "",
        placeholder: isRepo(input) ? "owner/repo" : (rules.Pattern || ""),
        autocomplete: "off",
      });
    }
    if (isRepo(input) && control.tagName === "INPUT") control.setAttribute("list", "run-repos");
    return { input, control, type };
  });

  const form = h("form", {
    id: "run-form",
    class: "stack",
    style: { gap: "14px" },
    onsubmit: (ev) => {
      ev.preventDefault();
      submit(closeFn);
    },
  },
  h("div", { class: "warning" },
    h("span", {}, "This starts a real run. It spends an agent pass and may post, push or comment, depending on the workflow."),
  ),
  fields.length
    ? fields.map(({ input, control }) => h("div", { class: "field" },
      h("label", { for: `run-${input.Name}` },
        input.Name,
        input.Required ? h("span", { class: "req" }, " *") : null,
        input.Type ? h("span", { class: "hint" }, ` (${input.Type})`) : null,
      ),
      control,
      input.Description ? h("span", { class: "hint" }, input.Description) : null,
    ))
    : h("p", { class: "muted" }, "This workflow takes no inputs."),
  repoList,
  );

  if (wantsRepos) {
    getJSON("/api/repos/names")
      .then((names) => mount(repoList, names.map((n) => h("option", { value: n }))))
      .catch(() => {});
  }

  const submit = async (close) => {
    const values = {};
    for (const { input, control, type } of fields) {
      const raw = control.value;
      if (raw === "" || raw == null) {
        if (input.Required) {
          toast(`${input.Name} is required`, { tone: "err" });
          control.focus();
          return;
        }
        continue;
      }
      if (type === "bool" || type === "boolean") values[input.Name] = raw === "true";
      else if (type === "integer" || type === "number") values[input.Name] = Number(raw);
      else values[input.Name] = raw;
    }
    try {
      const res = await awf(`/api/workflows/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/run`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ inputs: values }),
        timeout: 30000,
      });
      close();
      onStarted(res.body?.execution_id || "");
    } catch (err) {
      const detail = err.body?.error || err.message;
      toast(`Run refused: ${detail}`, { tone: "err", timeout: 9000 });
    }
  };

  const modal = openModal({
    title: `Run ${name}`,
    body: form,
    actions: [
      { label: "Cancel", class: "ghost", onClick: (close) => close() },
      { label: "Run now", class: "primary", id: "run-submit", onClick: submit },
    ],
  });
  closeFn = () => modal.close();

  const primary = modal.querySelector("#run-submit");
  const missing = () => fields.some(({ input, control }) => input.Required && control.value === "");
  const sync = () => { primary.disabled = missing(); };
  for (const { control } of fields) {
    control.addEventListener("input", sync);
    control.addEventListener("change", sync);
  }
  sync();
}
