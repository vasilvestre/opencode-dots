import { h, mount } from "../dom.js";
import { awf, getJSON } from "../api.js";
import { createEditor } from "../editor.js";
import { renderFlow } from "../flow.js";
import { navigate } from "../router.js";
import { confirmDialog } from "../modal.js";

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
    placeholder: "Filter workflows",
    "aria-label": "Filter workflows",
    oninput: () => renderList(),
  });
  const listEl = h("div", { class: "side-list" });
  const mainEl = h("div", {});
  const side = h("aside", { class: "side" },
    h("div", { class: "row between" },
      h("h2", {}, "Workflows"),
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
      title: "Discard unsaved changes?",
      message: `${selected} has edits that are not saved. Leaving drops them.`,
      confirm: "Discard",
      danger: true,
    });
  }

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
      mount(listEl, h("div", { class: "empty" }, workflows.length ? "Nothing matches." : "No workflow found."));
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
          if (w.name !== selected && !(await leaveOk())) return;
          navigate("workflows", w.name);
        },
      },
      h("b", {},
        w.name,
        !file?.exists ? h("span", { class: "warn-dot", title: "No YAML file in this repository" }, " ●") : null,
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
        `Cannot list workflows (${err.message}). Is awf running? Start it with make up.`));
    }
  }

  function showEmpty() {
    mount(mainEl, h("div", { class: "empty" }, "Pick a workflow on the left to read, edit or run it."));
  }

  async function open(name) {
    selected = name;
    renderList();
    const wf = workflows.find((w) => w.name === name);
    if (!wf) {
      mount(mainEl, h("div", { class: "empty" }, `No workflow named ${name}.`));
      return;
    }
    document.title = `${name} · dots`;
    const key = `${wf.scope}/${wf.name}`;
    const tabs = h("div", { class: "seg", role: "tablist", "aria-label": "View" },
      h("button", { role: "tab", "aria-selected": String(mode === "editor"), onclick: () => setMode("editor") }, "Editor"),
      h("button", { role: "tab", "aria-selected": String(mode === "split"), onclick: () => setMode("split") }, "Split"),
      h("button", { role: "tab", "aria-selected": String(mode === "graph"), onclick: () => setMode("graph") }, "Graph"),
    );

    mount(mainEl,
      h("div", { class: "card" },
        h("div", { class: "row between", style: { marginBottom: "6px" } },
          h("div", {},
            h("h1", {}, wf.name),
            h("div", { class: "file-meta" },
              h("span", { class: "badge" }, wf.scope),
              wf.version ? h("span", {}, `version ${wf.version}`) : null,
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
    const graphEl = h("div", {}, h("div", { class: "empty" }, "loading graph…"));
    const editorEl = h("div", {}, h("div", { class: "empty" }, "loading file…"));
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
            `The graph cannot be drawn from this file (${err.message}). Fix the YAML and save.`));
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
        `This workflow comes from the ${wf.scope} source. It is not a file in this repository, so the page cannot edit it.`));
      return;
    }
    editor?.destroy();
    editor = null;
    container.replaceChildren(h("div", { class: "empty" }, "loading file…"));
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
      }, "Reload from awf");
      mount(container,
        h("div", { class: "row between", style: { marginBottom: "14px" } },
          h("span", { class: "muted" }, "Click a step to see where it goes and what it runs. Red dots mark steps whose failure ends the run."),
          reloadBtn,
        ),
        renderFlow(detail),
      );
    } catch (err) {
      mount(container, h("div", { class: "empty" }, `Cannot load the graph (${err.message}).`));
    }
  }

  load();

  return {
    destroy() {
      destroyed = true;
      editor?.destroy();
      editor = null;
      document.title = "dots";
    },
  };
}

// Line (1-based) of a step key inside the top-level states: block, or 0.
function stepLine(text, step) {
  const lines = text.split("\n");
  let inSteps = false;
  for (let i = 0; i < lines.length; i++) {
    if (/^(steps|states):\s*$/.test(lines[i])) { inSteps = true; continue; }
    if (inSteps && /^\S/.test(lines[i]) && !lines[i].startsWith("#")) return 0;
    if (inSteps && lines[i].match(/^ {2}([^\s#:][^:]*):\s*$/)?.[1] === step) return i + 1;
  }
  return 0;
}

// Step whose block contains a 1-based line, or "" outside the states: block.
function stepAtLine(text, line) {
  const lines = text.split("\n");
  let inSteps = false;
  let current = "";
  for (let i = 0; i < Math.min(line, lines.length); i++) {
    if (/^(steps|states):\s*$/.test(lines[i])) { inSteps = true; current = ""; continue; }
    if (!inSteps) continue;
    if (/^\S/.test(lines[i]) && !lines[i].startsWith("#")) return "";
    const m = lines[i].match(/^ {2}([^\s#:][^:]*):\s*$/);
    if (m) current = m[1];
  }
  return current;
}
