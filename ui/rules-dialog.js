import { h, mount } from "./dom.js";
import { openModal } from "./modal.js";
import { t } from "./i18n.js";

// The rules of one feature on one repository, as a form instead of YAML. Each
// rule says when the workflow runs and for whose pull requests or issues. The
// dialog edits the keys it understands and hands every other key back as it
// found it, so nothing set in repos.yaml by hand is lost.

const EVENT_LABELS = {
  "pr-opened": t("rules-dialog.pr_opened"),
  "pr-updated": t("rules-dialog.pr_updated"),
  "checks-failed": t("rules-dialog.checks_failed"),
  "review-comment": t("rules-dialog.review_comment"),
  "issue-opened": t("rules-dialog.issue_opened"),
  "issue-labeled": t("rules-dialog.issue_labelled"),
  command: "/dots command",
};

const MANAGED = ["on", "label", "only_authors", "ignore_authors"];

export const eventLabel = (ev) => EVENT_LABELS[ev] || ev;

// ruleSummary says in one line what a rule does, for the table and the dialog.
export function ruleSummary(rule, form) {
  const parts = [];
  if (rule.label) parts.push(t("rules-dialog.issues_labelled", [rule.label]));
  else if (rule.on?.length) parts.push(rule.on.map(eventLabel).join(", "));
  else parts.push(t("common.default", [(form?.default_on || []).map(eventLabel).join(", ")]));
  if (rule.only_authors?.length) parts.push(t("rules-dialog.only_by", [rule.only_authors.join(", ")]));
  if (Array.isArray(rule.ignore_authors)) {
    parts.push(rule.ignore_authors.length ? t("rules-dialog.ignoring", [rule.ignore_authors.join(", ")]) : t("rules-dialog.ignoring_nobody"));
  } else if (form?.default_ignore?.length) {
    parts.push(t("rules-dialog.ignoring_bots_by_default", [form.default_ignore.length]));
  }
  return parts.join(" · ");
}

const splitList = (text) => text.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);

export function openRulesDialog({ repo, featureLabel, form, rules, onSave }) {
  const drafts = (rules.length ? rules : [{}]).map((r) => structuredClone(r));
  const list = h("div", { class: "stack", style: { gap: "12px" } });
  const error = h("div", { class: "issues bad", hidden: true });
  let modal = null;

  function ruleCard(rule, index) {
    const extras = Object.keys(rule).filter((k) => !MANAGED.includes(k));
    const preview = h("p", { class: "muted rule-preview" });
    const refresh = () => { preview.textContent = t("rules-dialog.this_rule_runs_on", [ruleSummary(rule, form)]); };

    const eventsBlock = h("div", { class: "field" },
      h("label", {}, t("rules-dialog.runs_when")),
      h("div", { class: "pills" }, form.events.map((ev) => {
        const box = h("input", { type: "checkbox", checked: (rule.on || []).includes(ev) });
        box.addEventListener("change", () => {
          const set = new Set(rule.on || []);
          if (box.checked) set.add(ev); else set.delete(ev);
          if (set.size) rule.on = form.events.filter((e) => set.has(e)); else delete rule.on;
          refresh();
        });
        return h("label", { class: "pill" }, box, eventLabel(ev));
      })),
      h("div", { class: "hint" },
        t("rules-dialog.nothing_ticked_means_the_default", [form.default_on.map(eventLabel).join(", ")])),
    );

    let labelBlock = null;
    if (form.label) {
      const input = h("input", { type: "text", value: rule.label || "", placeholder: t("rules-dialog.for_example_dot"), class: "mono" });
      input.addEventListener("input", () => {
        const v = input.value.trim();
        if (v) {
          rule.label = v;
          delete rule.on;
          for (const box of eventsBlock.querySelectorAll("input")) box.checked = false;
        } else delete rule.label;
        eventsBlock.hidden = Boolean(v);
        refresh();
      });
      eventsBlock.hidden = Boolean(rule.label);
      labelBlock = h("div", { class: "field" },
        h("label", {}, t("rules-dialog.only_issues_with_this_label")),
        input,
        h("div", { class: "hint" }, t("rules-dialog.leave_empty_to_act_on")));
    }

    const only = h("input", {
      type: "text", class: "mono", value: (rule.only_authors || []).join(", "),
      placeholder: t("rules-dialog.everyone_a_login_or_several"),
    });
    only.addEventListener("input", () => {
      const v = splitList(only.value);
      if (v.length) rule.only_authors = v; else delete rule.only_authors;
      refresh();
    });

    const mode = Array.isArray(rule.ignore_authors)
      ? (rule.ignore_authors.length ? "custom" : "nobody")
      : (form.default_ignore?.length ? "default" : "nobody");
    const custom = h("input", { type: "text", class: "mono", value: (rule.ignore_authors || []).join(", "), placeholder: "dependabot[bot], renovate[bot]" });
    const select = h("select", {},
      form.default_ignore?.length ? h("option", { value: "default" }, t("rules-dialog.the_default_list", [form.default_ignore.join(", ")])) : null,
      h("option", { value: "nobody" }, t("rules-dialog.nobody")),
      h("option", { value: "custom" }, t("rules-dialog.a_list_of_my_own")));
    select.value = mode;
    custom.hidden = mode !== "custom";
    const applyIgnore = () => {
      custom.hidden = select.value !== "custom";
      if (select.value === "default") delete rule.ignore_authors;
      else if (select.value === "nobody") rule.ignore_authors = [];
      else rule.ignore_authors = splitList(custom.value);
      refresh();
    };
    select.addEventListener("change", applyIgnore);
    custom.addEventListener("input", applyIgnore);
    if (mode === "default") delete rule.ignore_authors;

    refresh();
    return h("div", { class: "rule-card" },
      h("div", { class: "row between" },
        h("b", {}, drafts.length > 1 ? t("rules-dialog.rule", [index + 1]) : t("rules-dialog.when_it_runs")),
        drafts.length > 1
          ? h("button", { class: "btn ghost sm", onclick: () => { drafts.splice(index, 1); paint(); } }, t("rules-dialog.remove_this_rule"))
          : null),
      labelBlock,
      eventsBlock,
      h("div", { class: "field" }, h("label", {}, t("rules-dialog.only_for_authors")), only,
        h("div", { class: "hint" }, t("rules-dialog.limits_the_rule_to_pull"))),
      h("div", { class: "field" }, h("label", {}, t("rules-dialog.skip_authors")), select, custom),
      extras.length
        ? h("p", { class: "hint" }, t("rules-dialog.also_set_in_repos_yaml", [extras.join(", ")]))
        : null,
      preview,
    );
  }

  function paint() {
    mount(list, drafts.map(ruleCard),
      h("button", { class: "btn sm", style: { alignSelf: "flex-start" }, onclick: () => { drafts.push({}); paint(); } }, t("rules-dialog.add_another_rule")));
  }
  paint();

  function showError(message) {
    error.hidden = !message;
    mount(error, h("div", { class: "head" }, t("rules-dialog.repos_yaml_refuses_this")),
      h("ul", {}, String(message).split("\n").map((l) => h("li", {}, l))));
  }

  modal = openModal({
    title: `${featureLabel} on ${repo}`,
    width: "640px",
    body: h("div", { class: "stack", style: { gap: "14px" } },
      h("p", { class: "muted" },
        t("rules-dialog.each_rule_is_one_trigger")),
      list, error),
    actions: [
      { label: t("common.cancel"), class: "ghost", onClick: (close) => close() },
      {
        label: t("rules-dialog.save_rules"),
        class: "primary",
        onClick: async (close) => {
          showError("");
          try {
            await onSave(drafts);
            close();
          } catch (err) {
            showError(err.message);
          }
        },
      },
    ],
  });
  return modal;
}
