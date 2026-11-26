import { h, mount } from "./dom.js";
import { openModal } from "./modal.js";

// The rules of one feature on one repository, as a form instead of YAML. Each
// rule says when the workflow runs and for whose pull requests or issues. The
// dialog edits the keys it understands and hands every other key back as it
// found it, so nothing set in repos.yaml by hand is lost.

const EVENT_LABELS = {
  "pr-opened": "PR opened",
  "pr-updated": "PR updated",
  "checks-failed": "Checks failed",
  "review-comment": "Review comment",
  "issue-opened": "Issue opened",
  "issue-labeled": "Issue labelled",
  command: "/dots command",
};

const MANAGED = ["on", "label", "only_authors", "ignore_authors"];

export const eventLabel = (ev) => EVENT_LABELS[ev] || ev;

// ruleSummary says in one line what a rule does, for the table and the dialog.
export function ruleSummary(rule, form) {
  const parts = [];
  if (rule.label) parts.push(`issues labelled "${rule.label}"`);
  else if (rule.on?.length) parts.push(rule.on.map(eventLabel).join(", "));
  else parts.push(`default (${(form?.default_on || []).map(eventLabel).join(", ")})`);
  if (rule.only_authors?.length) parts.push(`only by ${rule.only_authors.join(", ")}`);
  if (Array.isArray(rule.ignore_authors)) {
    parts.push(rule.ignore_authors.length ? `ignoring ${rule.ignore_authors.join(", ")}` : "ignoring nobody");
  } else if (form?.default_ignore?.length) {
    parts.push(`ignoring ${form.default_ignore.length} bots by default`);
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
    const refresh = () => { preview.textContent = `This rule runs on: ${ruleSummary(rule, form)}`; };

    const eventsBlock = h("div", { class: "field" },
      h("label", {}, "Runs when"),
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
        `Nothing ticked means the default: ${form.default_on.map(eventLabel).join(", ")}.`),
    );

    let labelBlock = null;
    if (form.label) {
      const input = h("input", { type: "text", value: rule.label || "", placeholder: "for example dot", class: "mono" });
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
        h("label", {}, "Only issues with this label"),
        input,
        h("div", { class: "hint" }, "Leave empty to act on the events above. A label replaces them: the rule then runs when an issue gets it."));
    }

    const only = h("input", {
      type: "text", class: "mono", value: (rule.only_authors || []).join(", "),
      placeholder: "everyone (a login, or several separated by commas)",
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
      form.default_ignore?.length ? h("option", { value: "default" }, `The default list (${form.default_ignore.join(", ")})`) : null,
      h("option", { value: "nobody" }, "Nobody"),
      h("option", { value: "custom" }, "A list of my own"));
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
        h("b", {}, drafts.length > 1 ? `Rule ${index + 1}` : "When it runs"),
        drafts.length > 1
          ? h("button", { class: "btn ghost sm", onclick: () => { drafts.splice(index, 1); paint(); } }, "Remove this rule")
          : null),
      labelBlock,
      eventsBlock,
      h("div", { class: "field" }, h("label", {}, "Only for authors"), only,
        h("div", { class: "hint" }, "Limits the rule to pull requests or issues opened by these people.")),
      h("div", { class: "field" }, h("label", {}, "Skip authors"), select, custom),
      extras.length
        ? h("p", { class: "hint" }, `Also set in repos.yaml and kept as is: ${extras.join(", ")}.`)
        : null,
      preview,
    );
  }

  function paint() {
    mount(list, drafts.map(ruleCard),
      h("button", { class: "btn sm", style: { alignSelf: "flex-start" }, onclick: () => { drafts.push({}); paint(); } }, "Add another rule"));
  }
  paint();

  function showError(message) {
    error.hidden = !message;
    mount(error, h("div", { class: "head" }, "repos.yaml refuses this"),
      h("ul", {}, String(message).split("\n").map((l) => h("li", {}, l))));
  }

  modal = openModal({
    title: `${featureLabel} on ${repo}`,
    width: "640px",
    body: h("div", { class: "stack", style: { gap: "14px" } },
      h("p", { class: "muted" },
        "Each rule is one trigger of the workflow. Two rules mean it can start in two different ways."),
      list, error),
    actions: [
      { label: "Cancel", class: "ghost", onClick: (close) => close() },
      {
        label: "Save rules",
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
