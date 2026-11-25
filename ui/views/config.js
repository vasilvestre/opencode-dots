import { h, mount } from "../dom.js";
import { getJSON, putJSON } from "../api.js";
import { toast } from "../toast.js";
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
  let modelLoading = true;
  let modelSaving = false;
  let modelConflicts = false;
  let modelInputs = [];
  const listEl = h("div", {});
  const modelsEl = h("div", { class: "list", style: { marginTop: "14px" } },
    h("div", { class: "empty" }, "Loading model defaults…"));
  const saveModelsBtn = h("button", {
    class: "btn primary",
    disabled: true,
    onclick: saveModels,
  }, "Save");

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, "Config"),
        h("p", { class: "muted" }, "Files awf-gh and the workflows read at run time. Saves keep a stamped backup beside the file."),
      ),
      arg ? h("button", { class: "btn", onclick: () => navigate("config") }, "All files") : null,
    ),
    h("div", { class: "card" },
      h("div", { class: "row between" },
        h("h2", {}, "Model defaults"),
        saveModelsBtn,
      ),
      h("p", { class: "muted", style: { marginTop: "6px" } },
        "Use ", h("code", {}, "provider/model#variant"), ", for example ", h("code", {}, "local/helper#fast"),
        ". Changes apply to the next workflow run. Runs already in progress keep their current model."),
      modelsEl,
    ),
    listEl,
    h("div", { id: "config-editor" }),
  ));

  function updateModelsSave() {
    saveModelsBtn.disabled = modelLoading || modelSaving || modelConflicts || modelInputs.length === 0;
  }

  async function loadModels() {
    modelLoading = true;
    modelInputs = [];
    modelConflicts = false;
    updateModelsSave();
    mount(modelsEl, h("div", { class: "empty" }, "Loading model defaults…"));
    try {
      const res = await getJSON("/api/models");
      if (destroyed) return;
      const variables = res?.variables || [];
      const available = Array.isArray(res?.available) ? res.available : [];
      const catalog = available.length ? h("datalist", { id: "model-catalog" },
        available.map((model) => h("option", { value: model })),
      ) : null;
      const conflicts = variables.filter((v) => v.conflict);
      modelConflicts = conflicts.length > 0;
      modelInputs = variables.map((v, i) => h("input", {
        id: `model-default-${i}`,
        class: "mono",
        type: "text",
        value: v.value || "",
        maxLength: 200,
        autocomplete: "off",
        list: available.length ? "model-catalog" : null,
        placeholder: "provider/model#variant",
        dataset: { key: v.key },
        "aria-describedby": v.description ? `model-default-${i}-description` : null,
        style: { width: "100%" },
      }));
      mount(modelsEl,
        catalog,
        conflicts.length ? h("div", { class: "issues bad" },
          h("div", { class: "head" }, "Resolve model conflicts before saving"),
          h("ul", {}, conflicts.map((v) => h("li", {}, `${v.workflow}/${v.input}: ${v.conflict}`))),
        ) : null,
        variables.length ? variables.map((v, i) => h("div", { class: "field" },
          h("label", { for: `model-default-${i}` }, `${v.workflow} · ${v.input}`),
          modelInputs[i],
          v.description ? h("div", { class: "hint", id: `model-default-${i}-description` }, v.description) : null,
        )) : h("div", { class: "empty" }, "No model roles found."),
      );
    } catch (err) {
      if (destroyed) return;
      modelInputs = [];
      mount(modelsEl, h("div", { class: "empty" }, `Cannot load model defaults (${err.body?.error || err.message}).`));
      toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelLoading = false;
      if (!destroyed) updateModelsSave();
    }
  }

  async function saveModels() {
    if (modelLoading || modelSaving || modelConflicts || modelInputs.length === 0) return;
    modelSaving = true;
    updateModelsSave();
    modelInputs.forEach((input) => { input.disabled = true; });
    try {
      const variables = Object.fromEntries(modelInputs.map((input) => [input.dataset.key, input.value]));
      const res = await putJSON("/api/models", { variables });
      if (destroyed) return;
      const changed = res?.changed || [];
      const paths = changed.map((item) => typeof item === "string" ? item : item?.path).filter(Boolean).join(", ");
      toast(`Saved model defaults: ${changed.length} file${changed.length === 1 ? "" : "s"} changed${paths ? ` (${paths})` : ""}.`, { tone: "ok" });
    } catch (err) {
      if (!destroyed) toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelSaving = false;
      if (!destroyed) {
        modelInputs.forEach((input) => { input.disabled = false; });
        updateModelsSave();
      }
    }
  }

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

  loadModels();
  load();

  return {
    destroy() {
      destroyed = true;
      editor?.destroy();
      editor = null;
    },
  };
}
