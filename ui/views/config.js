import { h, mount } from "../dom.js";
import { getJSON, putJSON } from "../api.js";
import { toast } from "../toast.js";
import { createEditor } from "../editor.js";
import { bytes, when } from "../format.js";
import { confirmDialog } from "../modal.js";
import { setThemeMode, themeMode } from "../theme.js";
import { desktopState, isDesktopEnabled, requestDesktopPermission, setDesktopEnabled } from "../notifications.js";

const KIND_LABEL = {
  watcher: "watcher rules: which repository, which workflow, which events",
  list: "repositories visited by the daily pass, one per line",
  map: "host paths for the workflow shell steps",
  repos: "every repository and what the dot does on it",
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

  const section = arg === "notifications" ? "notifications" : arg === "appearance" ? "appearance" : (!arg || arg === "models") ? "models" : "file";
  let notifBase = { webhook: "", enabled: false };
  let files = [];
  let filesLoaded = false;

  const modelsPanel = h("div", { class: "card" },
    h("div", { class: "row between" },
      h("h2", {}, "Model defaults"),
      saveModelsBtn,
    ),
    h("p", { class: "muted", style: { marginTop: "6px" } },
      "Set the global model presets, then override individual roles only when needed. opencode takes ",
        h("code", {}, "provider/model#variant"), ", for example ", h("code", {}, "local/helper#fast"),
        ". The claude CLI takes its own names, for example ", h("code", {}, "sonnet"), " or ", h("code", {}, "claude-haiku-4-5"),
        "; a repository routed to ", h("code", {}, "provider: claude"), " uses the claude presets. Changes apply to the next workflow run. Runs already in progress keep their current model."),
      modelsEl,
  );
  const notifPanel = h("div", { class: "card" },
    h("div", { class: "row between" },
      h("h2", {}, "Notifications"),
      saveNotifBtn,
    ),
    h("p", { class: "muted", style: { marginTop: "6px" } },
      "The dashboard sends a Discord message when a run finishes. The browser only saves the URL here."),
    h("div", { class: "field", style: { marginTop: "12px" } },
      h("label", { for: "notif-webhook" }, "Discord webhook URL"),
      webhookInput,
    ),
    h("label", { class: "check", for: "notif-enabled" }, enabledInput, "Send a message when a run finishes"),
    h("div", { class: "field" },
      h("span", { class: "label" }, "Desktop notifications"),
      h("div", { class: "row" }, desktopStateEl, desktopBtn),
    ),
  );
  const themeSelect = h("select", {
    id: "theme-mode",
    onchange: () => { setThemeMode(themeSelect.value); toast("Theme saved in this browser.", { tone: "ok" }); },
  }, [["auto", "Auto (follow the system)"], ["dark", "Dark"], ["light", "Light"]].map(([v, l]) =>
    h("option", { value: v, selected: v === themeMode() }, l)));
  const appearancePanel = h("div", { class: "card" },
    h("h2", {}, "Appearance"),
    h("p", { class: "muted", style: { marginTop: "6px" } }, "Applies at once and is kept in this browser only."),
    h("div", { class: "field", style: { marginTop: "12px" } },
      h("label", { for: "theme-mode" }, "Theme"), themeSelect),
  );
  const editorPanel = h("div", { id: "config-editor" },
    h("div", { class: "empty" }, "loading file…"));

  const navEl = h("nav", { class: "side-list", "aria-label": "Settings sections" });
  const filesNav = h("div", { class: "side-list" });
  const generatedNav = h("div", {});
  const side = h("aside", { class: "side" },
    h("h2", {}, "Settings"),
    navEl,
    h("h3", { class: "muted", style: { marginTop: "6px" } }, "Local files"),
    filesNav,
    generatedNav,
  );
  modelsPanel.hidden = section !== "models";
  notifPanel.hidden = section !== "notifications";
  appearancePanel.hidden = section !== "appearance";
  editorPanel.hidden = section !== "file";
  mount(el, h("div", { class: "split" }, side,
    h("div", { class: "stack" },
      h("p", { class: "muted" }, "Dashboard preferences and the local files the stack reads at run time. Saves keep a stamped backup beside the file."),
      modelsPanel, notifPanel, appearancePanel, editorPanel)));

  const modelsDirty = () => modelInputs.some((i) => i.value !== i.dataset.initial)
    || modelRoles.some((r) => r.checkbox.checked !== (r.checkbox.dataset.initial === "1"));
  const notifDirty = () => webhookInput.value !== notifBase.webhook || enabledInput.checked !== notifBase.enabled;
  const anyDirty = () => modelsDirty() || notifDirty() || Boolean(editor?.isDirty());
  async function leaveOk() {
    if (!anyDirty()) return true;
    return confirmDialog({
      title: "Discard unsaved changes?",
      message: "This section has edits that are not saved. Leaving drops them.",
      confirm: "Discard",
      danger: true,
    });
  }
  const beforeUnload = (ev) => { if (anyDirty()) { ev.preventDefault(); ev.returnValue = ""; } };
  window.addEventListener("beforeunload", beforeUnload);

  function navItem(href, current, title, sub, dirty, warn) {
    return h("a", {
      class: "side-item",
      href,
      "aria-current": current ? "true" : null,
      onclick: async (ev) => {
        if (ev.metaKey || ev.ctrlKey || current) return;
        ev.preventDefault();
        if (await leaveOk()) location.hash = href;
      },
    },
    h("b", {}, title,
      dirty ? h("span", { class: "dirty-dot", style: { marginLeft: "6px" }, title: "Unsaved changes" }) : null,
      warn ? h("span", { class: "warn-dot", title: "Missing on disk" }, " ●") : null),
    sub ? h("span", {}, sub) : null);
  }
  function paintNav() {
    mount(navEl,
      navItem("#config/models", section === "models", "Model defaults", "which model each role uses", modelsDirty()),
      navItem("#config/notifications", section === "notifications", "Notifications", "Discord and desktop", notifDirty()),
      navItem("#config/appearance", section === "appearance", "Appearance", "light or dark theme", false));
    const item = (f) => navItem(`#config/${f.path}`, section === "file" && arg === f.path, f.path,
      f.generated ? "generated, read only" : KIND_LABEL[f.kind] || f.kind,
      section === "file" && arg === f.path && editor?.isDirty(), !f.exists);
    // repos.yaml is the file to edit; the files it generates stay readable.
    const own = files.filter((f) => !f.generated);
    const generated = files.filter((f) => f.generated);
    mount(filesNav,
      own.length ? own.map(item) : h("div", { class: "empty" }, filesLoaded ? "No local file." : "Loading…"));
    mount(generatedNav,
      generated.length ? h("h3", { class: "muted", style: { margin: "6px 0 4px" } }, "Generated from repos.yaml") : null,
      generated.length ? h("div", { class: "side-list" }, generated.map(item)) : null);
  }
  modelsEl.addEventListener("input", () => { updateModelsSave(); paintNav(); });
  webhookInput.addEventListener("input", () => { updateNotifSave(); paintNav(); });
  enabledInput.addEventListener("change", () => { updateNotifSave(); paintNav(); });
  paintNav();

  function updateModelsSave() {
    saveModelsBtn.disabled = modelLoading || modelSaving || modelConflicts || modelRoles.length === 0 || !modelsDirty();
    saveModelsBtn.title = saveModelsBtn.disabled && !modelLoading && !modelConflicts ? "No changes to save" : "";
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
      const hasClaude = variables.some((v) => v.provider === "claude");
      const claudeCatalog = h("datalist", { id: "claude-catalog" },
        ["sonnet", "haiku"].map((model) => h("option", { value: model })));
      presetInputs = {
        light: modelField("model-preset-light", presets.light, { preset: "light" }, catalogID),
        default: modelField("model-preset-default", presets.default, { preset: "default" }, catalogID),
      };
      if (hasClaude) {
        presetInputs.claude_light = modelField("model-preset-claude-light", presets.claude_light, { preset: "claude_light" }, "claude-catalog");
        presetInputs.claude_default = modelField("model-preset-claude-default", presets.claude_default, { preset: "claude_default" }, "claude-catalog");
      }
      modelRoles = variables.map((v, i) => {
        const enabled = v.override != null;
        const input = modelField(`model-override-${i}`, enabled ? v.override : "", { key: v.key }, v.provider === "claude" ? "claude-catalog" : catalogID);
        input.disabled = !enabled;
        const checkbox = h("input", {
          id: `model-override-${i}-enabled`,
          type: "checkbox",
          checked: enabled,
          onchange: () => {
            input.disabled = !checkbox.checked;
            if (checkbox.checked && !input.value) input.value = v.value || "";
            updateModelsSave();
            paintNav();
          },
        });
        return { variable: v, input, checkbox };
      });
      modelInputs = [...Object.values(presetInputs), ...modelRoles.map((role) => role.input)];
      modelInputs.forEach((input) => { input.dataset.initial = input.value; });
      modelRoles.forEach(({ checkbox }) => { checkbox.dataset.initial = checkbox.checked ? "1" : "0"; });
      const roleField = (v, i) => {
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
      };
      const rolesOf = (provider) => variables
        .map((v, i) => (v.provider === provider ? roleField(v, i) : null))
        .filter(Boolean);
      mount(modelsEl,
        catalog,
        hasClaude ? claudeCatalog : null,
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
        hasClaude ? h("h3", { style: { marginTop: "14px" } }, "claude presets") : null,
        hasClaude ? h("div", { class: "field" },
          h("label", { for: "model-preset-claude-default" }, "Claude default preset"),
          presetInputs.claude_default,
          h("div", { class: "hint" }, "Reviews when a repository is routed to provider claude."),
        ) : null,
        hasClaude ? h("div", { class: "field" },
          h("label", { for: "model-preset-claude-light" }, "Claude light preset"),
          presetInputs.claude_light,
          h("div", { class: "hint" }, "Review addressing when a repository is routed to provider claude. Keep it different from the default one."),
        ) : null,
        h("h3", { style: { marginTop: "14px" } }, "Role overrides (opencode)"),
        variables.length ? rolesOf("opencode") : h("div", { class: "empty" }, "No model roles found."),
        hasClaude ? h("h3", { style: { marginTop: "14px" } }, "Role overrides (claude)") : null,
        hasClaude ? rolesOf("claude") : null,
      );
    } catch (err) {
      if (destroyed) return;
      modelInputs = [];
      modelRoles = [];
      mount(modelsEl, h("div", { class: "empty" },
        `Cannot load model defaults (${err.body?.error || err.message}). `,
        h("button", { class: "btn sm", onclick: () => loadModels() }, "Retry")));
      toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelLoading = false;
      if (!destroyed) updateModelsSave();
    }
  }

  async function saveModels() {
    if (modelLoading || modelSaving || modelConflicts || modelRoles.length === 0 || !modelsDirty()) return;
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
    saveNotifBtn.disabled = notifLoading || notifSaving || !notifDirty();
    saveNotifBtn.title = saveNotifBtn.disabled && !notifLoading ? "No changes to save" : "";
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
      notifBase = { webhook: webhookInput.value, enabled: enabledInput.checked };
    } catch (err) {
      if (destroyed) return;
      toast(`Cannot load notification settings (${err.body?.error || err.message}). The form below shows empty values, not your saved ones.`, { tone: "err", timeout: 8000 });
      saveNotifBtn.disabled = true;
    } finally {
      notifLoading = false;
      if (!destroyed) updateNotifSave();
    }
  }

  async function saveNotifications() {
    if (notifLoading || notifSaving || !notifDirty()) return;
    notifSaving = true;
    updateNotifSave();
    webhookInput.disabled = true;
    enabledInput.disabled = true;
    try {
      await putJSON("/api/notifications", {
        discord: { enabled: enabledInput.checked, webhook: webhookInput.value },
      });
      if (!destroyed) {
        notifBase = { webhook: webhookInput.value, enabled: enabledInput.checked };
        paintNav();
        toast("Saved notification settings.", { tone: "ok" });
      }
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
    try {
      files = (await getJSON("/api/files")).filter((f) => f.kind !== "workflow");
    } catch (err) {
      mount(filesNav, h("div", { class: "empty" }, `Cannot list files (${err.message}).`));
      if (section === "file") mount(editorPanel, h("div", { class: "empty" }, "Cannot list the local files."));
      return;
    }
    if (destroyed) return;
    filesLoaded = true;
    paintNav();
    if (section !== "file") return;
    const selected = files.find((f) => f.path === arg);
    if (!selected) {
      mount(editorPanel, h("div", { class: "empty" }, `No local config named ${arg}.`));
      return;
    }
    await showEditor(selected);
  }

  async function showEditor(file) {
    editor?.destroy();
    editor = await createEditor({ path: file.path, kind: file.kind, readOnly: Boolean(file.generated) });
    if (destroyed) { editor.destroy(); return; }
    mount(editorPanel, h("div", { class: "card" },
      h("div", { class: "row between", style: { marginBottom: "10px" } },
        h("h2", {}, file.path),
        h("span", { class: "muted", style: { fontSize: "12px" } },
          file.exists ? `${bytes(file.size)} · edited ${when(file.mod_time)} · not in git` : "missing on disk: saving creates it"),
      ),
      file.generated
        ? h("p", { class: "warning", style: { marginBottom: "10px" } },
          "This file is written from repos.yaml. Change repos.yaml instead: saving it regenerates this file, and a hand edit here would be overwritten.")
        : null,
      editor.el,
    ));
    paintNav();
  }

  loadModels();
  loadNotifications();
  load();

  return {
    destroy() {
      destroyed = true;
      window.removeEventListener("beforeunload", beforeUnload);
      editor?.destroy();
      editor = null;
    },
  };
}
