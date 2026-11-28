import { h, mount } from "../dom.js";
import { getJSON, putJSON } from "../api.js";
import { toast } from "../toast.js";
import { createEditor } from "../editor.js";
import { bytes, when } from "../format.js";
import { confirmDialog } from "../modal.js";
import { setLeaveGuard } from "../router.js";
import { setThemeMode, themeMode } from "../theme.js";
import { desktopState, isDesktopEnabled, requestDesktopPermission, setDesktopEnabled } from "../notifications.js";
import { t, LANGS, langMode, setLang } from "../i18n.js";

const KIND_LABEL = {
  watcher: t("config.watcher_rules_which_repository_which"),
  list: t("config.repositories_visited_by_the_daily"),
  map: t("config.host_paths_for_the_workflow"),
  repos: t("config.every_repository_and_what_the"),
};

// Local configs are the files the agent stack reads from the repository root.
// They never leave this machine; the page still edits them like the workflows.
export function mountConfig(el, arg) {
  let destroyed = false;
  let editor = null;
  let modelLoading = true;
  let modelSaving = false;
  let modelInputs = [];
  let notifLoading = true;
  let notifSaving = false;
  let modelRoles = [];
  let presetInputs = {};
  const listEl = h("div", {});
  const modelsEl = h("div", { class: "list", style: { marginTop: "14px" } },
    h("div", { class: "empty" }, t("config.loading_model_defaults")));
  const saveModelsBtn = h("button", {
    class: "btn primary",
    disabled: true,
    onclick: saveModels,
  }, t("common.save"));
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
  }, t("common.save"));
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
      h("h2", {}, t("common.model_defaults")),
      saveModelsBtn,
    ),
    h("p", { class: "muted", style: { marginTop: "6px" } },
      t("config.models_hint_1"),
        h("code", {}, "provider/model#variant"), t("config.models_hint_2"), h("code", {}, "local/helper#fast"),
        t("config.models_hint_3"), h("code", {}, "sonnet"), t("config.models_hint_4"), h("code", {}, "claude-haiku-4-5"),
        t("config.models_hint_5"), h("code", {}, "provider: claude"), t("config.models_hint_6")),
      modelsEl,
  );
  const notifPanel = h("div", { class: "card" },
    h("div", { class: "row between" },
      h("h2", {}, t("common.notifications")),
      saveNotifBtn,
    ),
    h("p", { class: "muted", style: { marginTop: "6px" } },
      t("config.the_dashboard_sends_a_discord")),
    h("div", { class: "field", style: { marginTop: "12px" } },
      h("label", { for: "notif-webhook" }, t("config.discord_webhook_url")),
      webhookInput,
    ),
    h("label", { class: "check", for: "notif-enabled" }, enabledInput, t("config.send_a_message_when_a")),
    h("div", { class: "field" },
      h("span", { class: "label" }, t("config.desktop_notifications")),
      h("div", { class: "row" }, desktopStateEl, desktopBtn),
    ),
  );
  const themeSelect = h("select", {
    id: "theme-mode",
    onchange: () => { setThemeMode(themeSelect.value); toast(t("config.theme_saved_in_this_browser"), { tone: "ok" }); },
  }, [["auto", t("config.auto_follow_the_system")], ["dark", t("config.dark")], ["light", t("config.light")]].map(([v, l]) =>
    h("option", { value: v, selected: v === themeMode() }, l)));
  const langSelect = h("select", {
    id: "lang-mode",
    onchange: () => setLang(langSelect.value),
  }, LANGS.map(([v, l]) => h("option", { value: v, selected: v === langMode() }, v === "auto" ? t("config.language_auto") : l)));
  const appearancePanel = h("div", { class: "card" },
    h("h2", {}, t("common.appearance")),
    h("p", { class: "muted", style: { marginTop: "6px" } }, t("config.applies_at_once_and_is")),
    h("div", { class: "field", style: { marginTop: "12px" } },
      h("label", { for: "theme-mode" }, t("config.theme")), themeSelect),
    h("div", { class: "field", style: { marginTop: "12px" } },
      h("label", { for: "lang-mode" }, t("config.language")), langSelect,
      h("p", { class: "muted", style: { marginTop: "6px" } }, t("config.language_hint"))),
  );
  const editorPanel = h("div", { id: "config-editor" },
    h("div", { class: "empty" }, t("common.loading_file")));

  const navEl = h("nav", { class: "side-list", "aria-label": t("config.settings_sections") });
  const filesNav = h("div", { class: "side-list" });
  const generatedNav = h("div", {});
  const side = h("aside", { class: "side" },
    h("h2", {}, t("common.settings")),
    navEl,
    h("h3", { class: "muted", style: { marginTop: "6px" } }, t("config.local_files")),
    filesNav,
    generatedNav,
  );
  modelsPanel.hidden = section !== "models";
  notifPanel.hidden = section !== "notifications";
  appearancePanel.hidden = section !== "appearance";
  editorPanel.hidden = section !== "file";
  mount(el, h("div", { class: "split" }, side,
    h("div", { class: "stack" },
      h("p", { class: "muted" }, t("config.dashboard_preferences_and_the_local")),
      modelsPanel, notifPanel, appearancePanel, editorPanel)));

  const modelsDirty = () => modelInputs.some((i) => i.value !== i.dataset.initial)
    || modelRoles.some((r) => r.checkbox.checked !== (r.checkbox.dataset.initial === "1"));
  const notifDirty = () => webhookInput.value !== notifBase.webhook || enabledInput.checked !== notifBase.enabled;
  const anyDirty = () => modelsDirty() || notifDirty() || Boolean(editor?.isDirty());
  async function leaveOk() {
    if (!anyDirty()) return true;
    return confirmDialog({
      title: t("common.discard_unsaved_changes"),
      message: t("config.this_section_has_edits_that"),
      confirm: t("common.discard"),
      danger: true,
    });
  }
  const beforeUnload = (ev) => { if (anyDirty()) { ev.preventDefault(); ev.returnValue = ""; } };
  window.addEventListener("beforeunload", beforeUnload);
  setLeaveGuard(leaveOk);

  function navItem(href, current, title, sub, dirty, warn) {
    return h("a", {
      class: "side-item",
      href,
      "aria-current": current ? "true" : null,
    },
    h("b", {}, title,
      dirty ? h("span", { class: "dirty-dot", style: { marginLeft: "6px" }, title: t("common.unsaved_changes") }) : null,
      warn ? h("span", { class: "warn-dot", title: t("config.missing_on_disk") }, " ●") : null),
    sub ? h("span", {}, sub) : null);
  }
  function paintNav() {
    mount(navEl,
      navItem("#config/models", section === "models", t("common.model_defaults"), t("config.which_model_each_role_uses"), modelsDirty()),
      navItem("#config/notifications", section === "notifications", t("common.notifications"), t("config.discord_and_desktop"), notifDirty()),
      navItem("#config/appearance", section === "appearance", t("common.appearance"), t("config.light_or_dark_theme"), false));
    const item = (f) => navItem(`#config/${f.path}`, section === "file" && arg === f.path, f.path,
      f.generated ? (f.optional && !f.exists ? t("config.not_generated_no_rule_needs") : t("config.generated_read_only")) : KIND_LABEL[f.kind] || f.kind,
      section === "file" && arg === f.path && editor?.isDirty(), !f.exists && !f.optional);
    // repos.yaml is the file to edit; the files it generates stay readable.
    const own = files.filter((f) => !f.generated);
    const generated = files.filter((f) => f.generated);
    mount(filesNav,
      own.length ? own.map(item) : h("div", { class: "empty" }, filesLoaded ? t("config.no_local_file") : t("common.loading")));
    mount(generatedNav,
      generated.length ? h("h3", { class: "muted", style: { margin: "6px 0 4px" } }, t("config.generated_from_repos_yaml")) : null,
      generated.length ? h("div", { class: "side-list" }, generated.map(item)) : null);
  }
  modelsEl.addEventListener("input", () => { updateModelsSave(); paintNav(); });
  webhookInput.addEventListener("input", () => { updateNotifSave(); paintNav(); });
  enabledInput.addEventListener("change", () => { updateNotifSave(); paintNav(); });
  paintNav();

  function updateModelsSave() {
    saveModelsBtn.disabled = modelLoading || modelSaving || modelRoles.length === 0 || !modelsDirty();
    saveModelsBtn.title = saveModelsBtn.disabled && !modelLoading ? t("config.no_changes_to_save") : "";
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
    updateModelsSave();
    mount(modelsEl, h("div", { class: "empty" }, t("config.loading_model_defaults")));
    try {
      const res = await getJSON("/api/models");
      if (destroyed) return;
      const variables = res?.variables || [];
      const available = Array.isArray(res?.available) ? res.available : [];
      const presets = res?.presets || {};
      const catalog = available.length ? h("datalist", { id: "model-catalog" },
        available.map((model) => h("option", { value: model })),
      ) : null;
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
        role.input.setAttribute("aria-describedby", details);
        return h("div", { class: "field" },
          h("label", { for: role.checkbox.id }, role.checkbox, " Use a custom override"),
          role.input,
          h("div", { class: "hint", id: details },
            t("config.preset", [v.workflow, v.input]), h("code", {}, v.preset || "unassigned"),
            t("config.resolved"), h("code", {}, v.value || "")),
        );
      };
      const rolesOf = (provider) => variables
        .map((v, i) => (v.provider === provider ? roleField(v, i) : null))
        .filter(Boolean);
      mount(modelsEl,
        catalog,
        hasClaude ? claudeCatalog : null,
        h("div", { class: "field" },
          h("label", { for: "model-preset-light" }, t("config.light_preset")),
          presetInputs.light,
          h("div", { class: "hint" }, t("config.ask_ci_fixes_replies_and")),
        ),
        h("div", { class: "field" },
          h("label", { for: "model-preset-default" }, t("config.default_preset")),
          presetInputs.default,
          h("div", { class: "hint" }, t("config.issue_fixes_daily_tending_and")),
        ),
        hasClaude ? h("h3", { style: { marginTop: "14px" } }, "claude presets") : null,
        hasClaude ? h("div", { class: "field" },
          h("label", { for: "model-preset-claude-default" }, t("config.claude_default_preset")),
          presetInputs.claude_default,
          h("div", { class: "hint" }, t("config.reviews_when_a_repository_is")),
        ) : null,
        hasClaude ? h("div", { class: "field" },
          h("label", { for: "model-preset-claude-light" }, t("config.claude_light_preset")),
          presetInputs.claude_light,
          h("div", { class: "hint" }, t("config.review_addressing_when_a_repository")),
        ) : null,
        h("h3", { style: { marginTop: "14px" } }, t("config.role_overrides_opencode")),
        variables.length ? rolesOf("opencode") : h("div", { class: "empty" }, t("config.no_model_roles_found")),
        hasClaude ? h("h3", { style: { marginTop: "14px" } }, t("config.role_overrides_claude")) : null,
        hasClaude ? rolesOf("claude") : null,
      );
    } catch (err) {
      if (destroyed) return;
      modelInputs = [];
      modelRoles = [];
      mount(modelsEl, h("div", { class: "empty" },
        t("config.cannot_load_model_defaults", [err.body?.error || err.message]),
        h("button", { class: "btn sm", onclick: () => loadModels() }, t("common.retry"))));
      toast(err.body?.error || err.message, { tone: "err", timeout: 8000 });
    } finally {
      modelLoading = false;
      if (!destroyed) updateModelsSave();
    }
  }

  async function saveModels() {
    if (modelLoading || modelSaving || modelRoles.length === 0 || !modelsDirty()) return;
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
      toast(t("config.saved_model_defaults_file_changed", [changed.length, changed.length === 1 ? "" : "s", paths ? ` (${paths})` : ""]), { tone: "ok" });
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
    saveNotifBtn.title = saveNotifBtn.disabled && !notifLoading ? t("config.no_changes_to_save") : "";
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
      toast(t("config.cannot_load_notification_settings_the", [err.body?.error || err.message]), { tone: "err", timeout: 8000 });
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
        toast(t("config.saved_notification_settings"), { tone: "ok" });
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
    return isDesktopEnabled() ? t("config.disable_desktop_notifications") : t("config.enable_desktop_notifications");
  }

  function updateDesktop() {
    desktopStateEl.textContent = desktopState();
    desktopBtn.textContent = desktopLabel();
  }

  async function toggleDesktop() {
    if (isDesktopEnabled()) {
      setDesktopEnabled(false);
      updateDesktop();
      toast(t("config.desktop_notifications_disabled"), { tone: "ok" });
      return;
    }
    if (desktopState() === "unsupported") {
      toast(t("config.this_browser_does_not_support"), { tone: "err" });
      return;
    }
    let permission;
    try {
      permission = await requestDesktopPermission();
    } catch (err) {
      toast(t("config.cannot_request_notification_permission", [err.message]), { tone: "err" });
      return;
    }
    if (destroyed) return;
    setDesktopEnabled(permission === "granted");
    updateDesktop();
    if (permission === "granted") {
      toast(t("config.desktop_notifications_enabled"), { tone: "ok" });
    } else if (permission === "denied") {
      toast(t("config.notification_permission_denied_allow_it"), { tone: "err" });
    } else {
      toast(t("config.notification_permission_was_not_granted"), { tone: "info" });
    }
  }

  async function load() {
    try {
      files = (await getJSON("/api/files")).filter((f) => f.kind !== "workflow");
    } catch (err) {
      mount(filesNav, h("div", { class: "empty" }, t("config.cannot_list_files", [err.message])));
      if (section === "file") mount(editorPanel, h("div", { class: "empty" }, t("config.cannot_list_the_local_files")));
      return;
    }
    if (destroyed) return;
    filesLoaded = true;
    paintNav();
    if (section !== "file") return;
    const selected = files.find((f) => f.path === arg);
    if (!selected) {
      mount(editorPanel, h("div", { class: "empty" }, t("config.no_local_config_named", [arg])));
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
          file.exists ? t("config.edited_not_in_git", [bytes(file.size), when(file.mod_time)])
            : file.optional ? t("config.not_generated_no_repository_needs") : t("config.missing_on_disk_saving_creates")),
      ),
      file.generated
        ? h("p", { class: "warning", style: { marginBottom: "10px" } },
          t("config.this_file_is_written_from"))
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
      setLeaveGuard(null);
      editor?.destroy();
      editor = null;
    },
  };
}
