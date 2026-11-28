import { h, mount } from "../dom.js";
import { getJSON, putJSON } from "../api.js";
import { toast } from "../toast.js";
import { confirmDialog } from "../modal.js";
import { navigate, setLeaveGuard } from "../router.js";
import { setThemeMode, themeMode } from "../theme.js";
import { desktopState, isDesktopEnabled, requestDesktopPermission, setDesktopEnabled } from "../notifications.js";
import { t, LANGS, langMode, setLang } from "../i18n.js";
import { dotPanel } from "./dot-settings.js";

// Preferences only: model defaults, notifications, appearance. The repos.yaml
// files live in the Repos view; an old #config/<file> link is sent there.
export function mountConfig(el, arg) {
  if (arg && !["models", "notifications", "appearance", "dot"].includes(arg)) {
    navigate("repos", arg, { replace: true });
    return { destroy() {} };
  }
  let destroyed = false;
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

  const section = arg || "models";
  let notifBase = { webhook: "", enabled: false };

  const modelsPanel = h("div", { class: "card" },
    h("div", { class: "row between" },
      h("h2", {}, t("common.model_defaults")),
      saveModelsBtn,
    ),
    h("details", { class: "help" }, h("summary", {}, t("config.model_format_help")),
      h("p", { class: "muted" },
      t("config.models_hint_1"),
        h("code", {}, "provider/model#variant"), t("config.models_hint_2"), h("code", {}, "local/helper#fast"),
        t("config.models_hint_3"), h("code", {}, "sonnet"), t("config.models_hint_4"), h("code", {}, "claude-haiku-4-5"),
        t("config.models_hint_5"), h("code", {}, "provider: claude"), t("config.models_hint_6"))),
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
  const navEl = h("nav", { class: "side-list", "aria-label": t("config.settings_sections") });
  const side = h("aside", { class: "side" },
    h("h2", {}, t("common.settings")),
    navEl,
  );
  const dot = h("div", { class: "stack" }, h("h2", {}, t("dot.title")));
  if (section === "dot") dot.append(dotPanel());
  dot.hidden = section !== "dot";
  modelsPanel.hidden = section !== "models";
  notifPanel.hidden = section !== "notifications";
  appearancePanel.hidden = section !== "appearance";
  mount(el, h("div", { class: "split" }, side,
    h("div", { class: "stack" },
      h("p", { class: "muted" }, t("config.dashboard_preferences_and_the_local")),
      modelsPanel, dot, notifPanel, appearancePanel)));

  const modelsDirty = () => modelInputs.some((i) => i.value !== i.dataset.initial)
    || modelRoles.some((r) => r.checkbox.checked !== (r.checkbox.dataset.initial === "1"));
  const notifDirty = () => webhookInput.value !== notifBase.webhook || enabledInput.checked !== notifBase.enabled;
  const anyDirty = () => modelsDirty() || notifDirty();
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
      navItem("#config/dot", section === "dot", t("dot.title"), t("dot.nav_sub"), false),
      navItem("#config/notifications", section === "notifications", t("common.notifications"), t("config.discord_and_desktop"), notifDirty()),
      navItem("#config/appearance", section === "appearance", t("common.appearance"), t("config.light_or_dark_theme"), false));
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
          h("label", { for: role.checkbox.id }, role.checkbox, " ", t("config.use_custom_override")),
          role.input,
          h("div", { class: "hint", id: details },
            t("config.preset", [v.workflow, v.input]), h("code", {}, v.preset || "unassigned"),
            t("config.resolved"), h("code", {}, v.value || "")),
        );
      };
      const rolesOf = (provider) => variables
        .map((v, i) => (v.provider === provider ? roleField(v, i) : null))
        .filter(Boolean);
      const preset = (id, label, input, hint) => h("div", { class: "field" },
        h("label", { for: id }, label), input, h("div", { class: "hint" }, hint));
      const custom = modelRoles.filter((r) => r.checkbox.checked).length;
      const overrides = (provider, title) => {
        const list = rolesOf(provider);
        return list.length ? h("details", { class: "help" },
          h("summary", {}, title, ` (${variables.filter((v) => v.provider === provider).length})`), list) : null;
      };
      mount(modelsEl,
        catalog,
        hasClaude ? claudeCatalog : null,
        h("div", { class: hasClaude ? "grid-2" : "" },
          h("div", { class: "stack", style: { gap: "10px" } },
            h("h3", {}, "opencode"),
            preset("model-preset-light", t("config.light_preset"), presetInputs.light, t("config.ask_ci_fixes_replies_and")),
            preset("model-preset-default", t("config.default_preset"), presetInputs.default, t("config.issue_fixes_daily_tending_and"))),
          hasClaude ? h("div", { class: "stack", style: { gap: "10px" } },
            h("h3", {}, "claude"),
            preset("model-preset-claude-default", t("config.claude_default_preset"), presetInputs.claude_default, t("config.reviews_when_a_repository_is")),
            preset("model-preset-claude-light", t("config.claude_light_preset"), presetInputs.claude_light, t("config.review_addressing_when_a_repository"))) : null),
        variables.length ? h("div", { class: "stack", style: { gap: "6px", marginTop: "14px" } },
          h("h3", {}, t("config.role_overrides"), custom ? h("span", { class: "badge warn", style: { marginLeft: "8px" } }, t("config.n_custom", [custom])) : null),
          overrides("opencode", "opencode"),
          hasClaude ? overrides("claude", "claude") : null) : h("div", { class: "empty" }, t("config.no_model_roles_found")),
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

  loadModels();
  loadNotifications();

  return {
    destroy() {
      destroyed = true;
      window.removeEventListener("beforeunload", beforeUnload);
      setLeaveGuard(null);
    },
  };
}
