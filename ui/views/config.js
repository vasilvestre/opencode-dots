import { h, mount } from "../dom.js";
import { getJSON, putJSON } from "../api.js";
import { toast } from "../toast.js";
import { createEditor } from "../editor.js";
import { bytes, when } from "../format.js";
import { navigate } from "../router.js";
import { desktopState, isDesktopEnabled, requestDesktopPermission, setDesktopEnabled } from "../notifications.js";

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
  let notifLoading = true;
  let notifSaving = false;
  let modelRoles = [];
  let presetInputs = {};
  const listEl = h("div", {});
  const modelsEl = h("div", { class: "list", style: { marginTop: "14px" } },
    h("div", { class: "empty" }, "Loading model defaults…"));
  const saveModelsBtn = h("button", {
    class: "btn primary",
    disabled: true,
    onclick: saveModels,
  }, "Save");
  const webhookInput = h("input", {
    id: "notif-webhook",
    class: "mono",
    type: "text",
    maxLength: 500,
    autocomplete: "off",
    placeholder: "https://discord.com/api/webhooks/...",
    style: { width: "100%" },
  });
  const enabledInput = h("input", { id: "notif-enabled", type: "checkbox" });
  const saveNotifBtn = h("button", {
    class: "btn primary",
    disabled: true,
    onclick: saveNotifications,
  }, "Save");
  const desktopStateEl = h("span", { class: "muted" }, desktopState());
  const desktopBtn = h("button", {
    class: "btn",
    style: { marginLeft: "8px" },
    onclick: toggleDesktop,
  }, desktopLabel());

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
        "Set two global model presets, then override individual roles only when needed. Use ",
        h("code", {}, "provider/model#variant"), ", for example ", h("code", {}, "local/helper#fast"),
        ". Changes apply to the next workflow run. Runs already in progress keep their current model."),
      modelsEl,
    ),
    h("div", { class: "card" },
      h("div", { class: "row between" },
        h("h2", {}, "Notifications"),
        saveNotifBtn,
      ),
      h("p", { class: "muted", style: { marginTop: "6px" } },
        "The dashboard sends a Discord message when a run finishes. The browser only saves the URL here."),
      h("div", { class: "field" },
        h("label", { for: "notif-webhook" }, "Discord webhook URL"),
        webhookInput,
      ),
      h("div", { class: "field" },
        h("label", { for: "notif-enabled" }, "Enabled"),
        enabledInput,
      ),
      h("div", { class: "field" },
        h("span", {}, "Desktop notifications: "),
        desktopStateEl,
        desktopBtn,
      ),
    ),
    listEl,
    h("div", { id: "config-editor" }),
  ));

  function updateModelsSave() {
    saveModelsBtn.disabled = modelLoading || modelSaving || modelConflicts || modelRoles.length === 0;
  }

  function modelField(id, value, dataset, list = null) {
    return h("input", {
      id,
      class: "mono",
      type: "text",
      value: value || "",
      maxLength: 200,
      autocomplete: "off",
      list,
      placeholder: "provider/model#variant",
      dataset,
      style: { width: "100%" },
    });
  }

  async function loadModels() {
    modelLoading = true;
    modelInputs = [];
    modelRoles = [];
    presetInputs = {};
    modelConflicts = false;
    updateModelsSave();
    mount(modelsEl, h("div", { class: "empty" }, "Loading model defaults…"));
    try {
      const res = await getJSON("/api/models");
      if (destroyed) return;
      const variables = res?.variables || [];
      const available = Array.isArray(res?.available) ? res.available : [];
      const presets = res?.presets || {};
      const problems = Array.isArray(res?.problems) ? res.problems : [];
      const catalog = available.length ? h("datalist", { id: "model-catalog" },
        available.map((model) => h("option", { value: model })),
      ) : null;
      const conflicts = variables.filter((v) => v.conflict);
      modelConflicts = conflicts.length > 0 || problems.length > 0;
      const catalogID = available.length ? "model-catalog" : null;
      presetInputs = {
        light: modelField("model-preset-light", presets.light, { preset: "light" }, catalogID),
        default: modelField("model-preset-default", presets.default, { preset: "default" }, catalogID),
      };
      modelRoles = variables.map((v, i) => {
        const enabled = v.override != null;
        const input = modelField(`model-override-${i}`, enabled ? v.override : "", { key: v.key }, catalogID);
        input.disabled = !enabled;
        const checkbox = h("input", {
          id: `model-override-${i}-enabled`,
          type: "checkbox",
          checked: enabled,
          onchange: () => {
            input.disabled = !checkbox.checked;
            if (checkbox.checked && !input.value) input.value = v.value || "";
          },
        });
        return { variable: v, input, checkbox };
      });
      modelInputs = [...Object.values(presetInputs), ...modelRoles.map((role) => role.input)];
      mount(modelsEl,
        catalog,
        res?.migration_required ? h("div", { class: "issues" },
          h("div", { class: "head" }, "Model presets need a first save"),
          h("p", { class: "hint", style: { padding: "0 12px 10px" } },
            "Current workflow defaults were imported in memory. Save to create model-presets.yaml."),
        ) : null,
        problems.length ? h("div", { class: "issues bad" },
          h("div", { class: "head" }, "Resolve model configuration problems before saving"),
          h("ul", {}, problems.map((problem) => h("li", {}, problem))),
        ) : null,
        conflicts.length ? h("div", { class: "issues bad" },
          h("div", { class: "head" }, "Resolve model conflicts before saving"),
          h("ul", {}, conflicts.map((v) => h("li", {}, `${v.workflow}/${v.input}: ${v.conflict}`))),
        ) : null,
        h("div", { class: "field" },
          h("label", { for: "model-preset-light" }, "Light preset"),
          presetInputs.light,
          h("div", { class: "hint" }, "Ask, CI fixes, replies, and review addressing."),
        ),
        h("div", { class: "field" },
          h("label", { for: "model-preset-default" }, "Default preset"),
          presetInputs.default,
          h("div", { class: "hint" }, "Issue fixes, daily tending, and reviews."),
        ),
        h("h3", { style: { marginTop: "14px" } }, "Role overrides"),
        variables.length ? variables.map((v, i) => {
          const role = modelRoles[i];
          const details = `model-role-${i}-details`;
          const conflict = `model-role-${i}-conflict`;
          role.input.setAttribute("aria-describedby", v.conflict ? `${details} ${conflict}` : details);
          return h("div", { class: "field" },
            h("label", { for: role.checkbox.id }, role.checkbox, " Use a custom override"),
            role.input,
            h("div", { class: "hint", id: details },
              `${v.workflow} · ${v.input} · preset: `, h("code", {}, v.preset || "unassigned"),
              " · resolved: ", h("code", {}, v.value || "")),
            v.conflict ? h("div", { class: "hint", id: conflict, style: { color: "var(--err)" } }, v.conflict) : null,
            v.description ? h("div", { class: "hint" }, v.description) : null,
          );
        }) : h("div", { class: "empty" }, "No model roles found."),
      );
    } catch (err) {
      if (destroyed) return;
      modelInputs = [];
      modelRoles = [];
      mount(modelsEl, h("div", { class: "empty" }, `Cannot load model defaults (${err.body?.error || err.message}).`));
      toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelLoading = false;
      if (!destroyed) updateModelsSave();
    }
  }

  async function saveModels() {
    if (modelLoading || modelSaving || modelConflicts || modelRoles.length === 0) return;
    modelSaving = true;
    updateModelsSave();
    modelInputs.forEach((input) => { input.disabled = true; });
    modelRoles.forEach(({ checkbox }) => { checkbox.disabled = true; });
    try {
      const presets = Object.fromEntries(Object.entries(presetInputs).map(([name, input]) => [name, input.value]));
      const overrides = Object.fromEntries(modelRoles.map(({ variable, input, checkbox }) =>
        [variable.key, checkbox.checked ? input.value : null]));
      const res = await putJSON("/api/models", { presets, overrides });
      if (destroyed) return;
      const changed = res?.changed || [];
      const paths = changed.map((item) => typeof item === "string" ? item : item?.path).filter(Boolean).join(", ");
      toast(`Saved model defaults: ${changed.length} file${changed.length === 1 ? "" : "s"} changed${paths ? ` (${paths})` : ""}.`, { tone: "ok" });
      await loadModels();
    } catch (err) {
      if (!destroyed) toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelSaving = false;
      if (!destroyed) {
        modelInputs.forEach((input) => { input.disabled = false; });
        modelRoles.forEach(({ input, checkbox }) => {
          checkbox.disabled = false;
          input.disabled = !checkbox.checked;
        });
        updateModelsSave();
      }
    }
  }

  function updateNotifSave() {
    saveNotifBtn.disabled = notifLoading || notifSaving;
  }

  async function loadNotifications() {
    notifLoading = true;
    updateNotifSave();
    try {
      const res = await getJSON("/api/notifications");
      if (destroyed) return;
      const discord = res?.discord || {};
      webhookInput.value = discord.webhook || "";
      enabledInput.checked = Boolean(discord.enabled);
    } catch (err) {
      if (destroyed) return;
      toast(`Cannot load notification settings (${err.body?.error || err.message}).`, { tone: "err", timeout: 8000 });
    } finally {
      notifLoading = false;
      if (!destroyed) updateNotifSave();
    }
  }

  async function saveNotifications() {
    if (notifLoading || notifSaving) return;
    notifSaving = true;
    updateNotifSave();
    webhookInput.disabled = true;
    enabledInput.disabled = true;
    try {
      await putJSON("/api/notifications", {
        discord: { enabled: enabledInput.checked, webhook: webhookInput.value },
      });
      if (!destroyed) toast("Saved notification settings.", { tone: "ok" });
    } catch (err) {
      if (!destroyed) toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      notifSaving = false;
      if (!destroyed) {
        webhookInput.disabled = false;
        enabledInput.disabled = false;
        updateNotifSave();
      }
    }
  }

  function desktopLabel() {
    return isDesktopEnabled() ? "Disable desktop notifications" : "Enable desktop notifications";
  }

  function updateDesktop() {
    desktopStateEl.textContent = desktopState();
    desktopBtn.textContent = desktopLabel();
  }

  async function toggleDesktop() {
    if (isDesktopEnabled()) {
      setDesktopEnabled(false);
      updateDesktop();
      toast("Desktop notifications disabled.", { tone: "ok" });
      return;
    }
    if (desktopState() === "unsupported") {
      toast("This browser does not support desktop notifications.", { tone: "err" });
      return;
    }
    let permission;
    try {
      permission = await requestDesktopPermission();
    } catch (err) {
      toast(`Cannot request notification permission: ${err.message}`, { tone: "err" });
      return;
    }
    if (destroyed) return;
    setDesktopEnabled(permission === "granted");
    updateDesktop();
    if (permission === "granted") {
      toast("Desktop notifications enabled.", { tone: "ok" });
    } else if (permission === "denied") {
      toast("Notification permission denied. Allow it in your browser settings.", { tone: "err" });
    } else {
      toast("Notification permission was not granted.", { tone: "info" });
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
  loadNotifications();
  load();

  return {
    destroy() {
      destroyed = true;
      editor?.destroy();
      editor = null;
    },
  };
}
