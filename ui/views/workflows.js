import { h, mount } from "../dom.js";
import { awf, getJSON } from "../api.js";
import { createEditor } from "../editor.js";
import { renderFlow } from "../flow.js";
import { navigate, setLeaveGuard } from "../router.js";
import { confirmDialog } from "../modal.js";
import { t } from "../i18n.js";

// Workflows are read through awf, edited on disk. The list pairs each awf
// entry with its file so the page knows what it can actually write.
export function mountWorkflows(el, arg) {
  let destroyed = false;
  let workflows = [];
  let editable = new Map();
  let selected = "";
  let mode = "split";
  let editor = null;
  const detailCache = new Map();

  const search = h("input", {
    class: "search",
    type: "search",
    placeholder: t("workflows.filter_workflows"),
    "aria-label": t("workflows.filter_workflows"),
    oninput: () => renderList(),
  });
  const listEl = h("div", { class: "side-list" });
  const mainEl = h("div", {});
  const side = h("aside", { class: "side" },
    h("div", { class: "row between" },
      h("h2", {}, t("common.workflows")),
      h("span", { class: "muted", id: "wf-count" }, ""),
    ),
    search,
    listEl,
  );
  mount(el, h("div", { class: "split" }, side, mainEl));

  // The editor holds text the disk does not: ask before dropping it.
  async function leaveOk() {
    if (!editor?.isDirty()) return true;
    return confirmDialog({
      title: t("common.discard_unsaved_changes"),
      message: t("workflows.has_edits_that_are_not", [selected]),
      confirm: t("common.discard"),
      danger: true,
    });
  }

  setLeaveGuard(leaveOk);

  function renderList() {
    const q = search.value.trim().toLowerCase();
    // A leading underscore marks a workflow only another workflow calls. Hide it
    // unless the filter itself starts with an underscore.
    const rows = workflows.filter((w) =>
      (!w.name.startsWith("_") || q.startsWith("_")) &&
      (!q || w.name.toLowerCase().includes(q) || (w.description || "").toLowerCase().includes(q)));
    const count = el.querySelector("#wf-count");
    if (count) count.textContent = `${rows.length}`;
    if (!rows.length) {
      mount(listEl, h("div", { class: "empty" }, workflows.length ? t("common.nothing_matches") : t("workflows.no_workflow_found")));
      return;
    }
    mount(listEl, rows.map((w) => {
      const file = editable.get(`workflows/${w.name}.yaml`);
      return h("a", {
        class: "side-item",
        href: `#workflows/${encodeURIComponent(w.name)}`,
        "aria-current": w.name === selected ? "true" : null,
        onclick: async (ev) => {
          if (ev.metaKey || ev.ctrlKey) return;
          ev.preventDefault();
          navigate("workflows", w.name);
        },
      },
      h("b", {},
        w.name,
        !file?.exists ? h("span", { class: "warn-dot", title: t("workflows.no_yaml_file_in_this") }, " ●") : null,
      ),
      h("span", {}, `${w.scope}${w.description ? ` · ${w.description}` : ""}`),
      );
    }));
  }

  async function load() {
    try {
      const [wfRes, files] = await Promise.all([
        awf("/api/workflows"),
        getJSON("/api/files").catch(() => []),
      ]);
      workflows = (wfRes.body?.workflows || []).slice().sort((a, b) => a.name.localeCompare(b.name));
      editable = new Map(files.map((f) => [f.path, f]));
      renderList();
      if (arg) open(arg);
      else showEmpty();
    } catch (err) {
      mount(mainEl, h("div", { class: "empty" },
        t("workflows.cannot_list_workflows_is_awf", [err.message])));
    }
  }

  function showEmpty() {
    mount(mainEl, h("div", { class: "empty" }, t("workflows.pick_a_workflow_on_the")));
  }

  async function open(name) {
    selected = name;
    renderList();
    const wf = workflows.find((w) => w.name === name);
    if (!wf) {
      mount(mainEl, h("div", { class: "empty" }, t("workflows.no_workflow_named", [name])));
      return;
    }
    document.title = `${name} · dots`;
    const key = `${wf.scope}/${wf.name}`;
    const tabs = h("div", { class: "seg", role: "tablist", "aria-label": t("workflows.view") },
      h("button", { role: "tab", "aria-selected": String(mode === "editor"), onclick: () => setMode("editor") }, t("workflows.editor")),
      h("button", { role: "tab", "aria-selected": String(mode === "split"), onclick: () => setMode("split") }, t("workflows.split")),
      h("button", { role: "tab", "aria-selected": String(mode === "graph"), onclick: () => setMode("graph") }, t("workflows.graph")),
    );

    mount(mainEl,
      h("div", { class: "card" },
        h("div", { class: "row between", style: { marginBottom: "6px" } },
          h("div", {},
            h("h1", {}, wf.name),
            h("div", { class: "file-meta" },
              h("span", { class: "badge" }, wf.scope),
              wf.version ? h("span", {}, t("common.version", [wf.version])) : null,
              h("span", { class: "mono" }, `workflows/${wf.name}.yaml`),
            ),
          ),
          tabs,
        ),
        wf.description ? h("p", { class: "muted", style: { marginTop: "10px" } }, wf.description) : null,
      ),
      h("div", { class: "card", id: "wf-content" }, h("div", { class: "empty" }, "loading…")),
    );

    const content = el.querySelector("#wf-content");
    if (mode === "editor") await showEditor(content, wf);
    else if (mode === "graph") await showGraph(content, key);
    else await showSplit(content, wf, key);
  }

  async function setMode(next) {
    if (mode === next) return;
    if (!(await leaveOk())) return;
    mode = next;
    open(selected);
  }

  // Editor and graph side by side. A click on a step selects its YAML line;
  // moving the cursor into a step selects it in the graph. Saving refreshes
  // the graph from awf, which re-reads the file.
  async function showSplit(container, wf, cacheKey) {
    const file = editable.get(`workflows/${wf.name}.yaml`);
    if (!file?.exists) {
      await showGraph(container, cacheKey);
      return;
    }
    editor?.destroy();
    editor = null;
    const graphEl = h("div", {}, h("div", { class: "empty" }, t("workflows.loading_graph")));
    const editorEl = h("div", {}, h("div", { class: "empty" }, t("common.loading_file")));
    mount(container, h("div", { class: "wf-split" }, editorEl, graphEl));
    let flow = null;
    let ed = null;

    const paintGraph = async (fresh) => {
      if (fresh) detailCache.delete(cacheKey);
      try {
        let detail = detailCache.get(cacheKey);
        if (!detail) {
          const [scope, name] = cacheKey.split("/");
          detail = (await awf(`/api/workflows/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`)).body;
          detailCache.set(cacheKey, detail);
        }
        if (!graphEl.isConnected) return;
        flow = renderFlow(detail, {
          compact: true,
          onSelect: (step) => {
            const line = stepLine(ed?.text() || "", step);
            if (line) ed.gotoLine(line);
          },
        });
        mount(graphEl, flow);
      } catch (err) {
        if (graphEl.isConnected) {
          mount(graphEl, h("div", { class: "empty" },
            t("workflows.the_graph_cannot_be_drawn", [err.message])));
        }
      }
    };

    ed = await createEditor({
      path: `workflows/${wf.name}.yaml`,
      kind: "workflow",
      scope: wf.scope,
      name: wf.name,
      onSave: () => paintGraph(true),
      onCursor: (line, text) => {
        const step = stepAtLine(text, line);
        if (step) flow?.selectStep(step);
      },
    });
    if (destroyed || selected !== wf.name || !editorEl.isConnected) { ed.destroy(); return; }
    editor = ed;
    mount(editorEl, ed.el);
    await paintGraph(false);
  }

  async function showEditor(container, wf) {
    const file = editable.get(`workflows/${wf.name}.yaml`);
    if (!file?.exists) {
      mount(container, h("div", { class: "empty" },
        t("workflows.this_workflow_comes_from_the", [wf.scope])));
      return;
    }
    editor?.destroy();
    editor = null;
    container.replaceChildren(h("div", { class: "empty" }, t("common.loading_file")));
    const ed = await createEditor({
      path: `workflows/${wf.name}.yaml`,
      kind: "workflow",
      scope: wf.scope,
      name: wf.name,
    });
    if (destroyed || selected !== wf.name) { ed.destroy(); return; }
    editor = ed;
    if (!container.isConnected) return;
    mount(container, ed.el);
  }

  async function showGraph(container, cacheKey) {
    editor?.destroy();
    editor = null;
    try {
      let detail = detailCache.get(cacheKey);
      if (!detail) {
        detail = (await awf(`/api/workflows/${encodeURIComponent(cacheKey.split("/")[0])}/${encodeURIComponent(cacheKey.split("/")[1])}`)).body;
        detailCache.set(cacheKey, detail);
      }
      if (!container.isConnected) return;
      const reloadBtn = h("button", {
        class: "btn sm",
        onclick: () => { detailCache.delete(cacheKey); showGraph(container, cacheKey); },
      }, t("workflows.reload_from_awf"));
      mount(container,
        h("div", { class: "row between", style: { marginBottom: "14px" } },
          h("span", { class: "muted" }, t("workflows.click_a_step_to_see")),
          reloadBtn,
        ),
        renderFlow(detail),
      );
    } catch (err) {
      mount(container, h("div", { class: "empty" }, t("workflows.cannot_load_the_graph", [err.message])));
    }
  }

  load();

  return {
    destroy() {
      destroyed = true;
      setLeaveGuard(null);
      editor?.destroy();
      editor = null;
      document.title = "dots";
    },
  };
}

// Step keys of the top-level steps:/states: block, in file order, each with its
// 1-based line. An entry with an empty name marks where a block ends. The key
// indent is whatever the block's first line uses, and the `initial:` pointer
// is not a step.
function stepKeys(text) {
  const keys = [];
  let inSteps = false;
  let indent = 0;
  text.split("\n").forEach((line, i) => {
    if (/^(steps|states):\s*$/.test(line)) {
      inSteps = true;
      indent = 0;
      keys.push({ name: "", line: i + 1 });
      return;
    }
    if (!inSteps || !line.trim() || line.trimStart().startsWith("#")) return;
    if (/^\S/.test(line)) {
      inSteps = false;
      keys.push({ name: "", line: i + 1 });
      return;
    }
    if (!indent) indent = line.length - line.trimStart().length;
    const m = line.match(/^( +)(?!initial:\s+[^\s#{])([^\s#:][^:]*):(?:\s.*)?$/);
    if (m && m[1].length === indent) keys.push({ name: m[2], line: i + 1 });
  });
  return keys;
}

// Line (1-based) of a step key inside the top-level states: block, or 0.
function stepLine(text, step) {
  return stepKeys(text).find((k) => k.name === step)?.line || 0;
}

// Step whose block contains a 1-based line, or "" outside the states: block.
function stepAtLine(text, line) {
  return stepKeys(text).filter((k) => k.line <= line).pop()?.name || "";
}
