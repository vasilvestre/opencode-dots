import { h, mount } from "../dom.js";
import { getJSON, patchJSON } from "../api.js";
import { toast } from "../toast.js";
import { t } from "../i18n.js";

const AUTHOR_FEATURES = ["review", "reply", "fix_issue", "fix_ci"];
const LABEL = { review: "common.review", reply: "repos.reply", fix_issue: "repos.fix_issue", fix_ci: "repos.fix_ci" };

// The settings of this machine's dot: how much one pass takes, which dot it is, who the watcher
// ignores or listens to by default, and (read only) what it is allowed to reach. Each edit writes
// repos.yaml at once, like the Repositories page.
export function dotPanel() {
  const el = h("div", { class: "stack" }, h("div", { class: "empty" }, t("common.loading")));
  let cfg = null;
  let chain = Promise.resolve();

  function save(payload) {
    const run = chain.then(async () => {
      try {
        cfg = (await patchJSON("/api/repos/config", payload)).config;
        paint();
      } catch (err) {
        toast(err.body?.error || err.message, { tone: "err", timeout: 9000 });
        await load();
      }
    });
    chain = run.catch(() => {});
    return run;
  }

  function numberField(value, placeholder, onSave) {
    const input = h("input", { type: "number", min: "1", max: "50", step: "1", value: value || "", placeholder, style: { width: "110px" } });
    input.addEventListener("change", () => onSave(input.value === "" ? null : Number(input.value)));
    return input;
  }

  function textField(value, placeholder, onSave) {
    const input = h("input", { type: "text", class: "mono", value: value || "", placeholder, style: { width: "200px" } });
    input.addEventListener("change", () => onSave(input.value.trim() || null));
    return input;
  }

  function chipList(values, placeholder, onChange) {
    const input = h("input", { type: "text", class: "mono", placeholder, style: { width: "150px" } });
    const add = () => {
      const name = input.value.trim();
      if (!name) return;
      if (values.includes(name)) { toast(t("repos.source_already", [name]), { tone: "err" }); return; }
      onChange([...values, name]);
    };
    input.addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); add(); } });
    return h("div", { class: "chips", style: { alignItems: "center" } },
      values.map((name) => h("span", { class: "chip" }, name,
        h("button", { class: "chip-x", "aria-label": t("repos.source_remove", [name]), onclick: () => onChange(values.filter((v) => v !== name)) }, "×"))),
      input, h("button", { class: "btn sm", onclick: add }, t("repos.source_add")));
  }

  const row = (label, hint, control) => h("div", { class: "feat-row on" },
    h("div", { class: "feat-text" }, h("b", {}, label), h("div", { class: "muted" }, hint)), control);

  const list = (v) => (v && v.length ? v.join(", ") : "—");

  function paint() {
    const s = cfg.settings || {};
    const trust = s.trust || {};
    const allow = trust.allow || {};
    const execs = Object.entries(s.executors || {});
    mount(el,
      h("p", { class: "muted" }, t("dot.intro")),
      h("div", { class: "card feat-group" },
        h("h3", { class: "muted" }, t("dot.group_run")),
        row(t("dot.max_per_run"), t("dot.max_per_run_hint"),
          numberField(s.max_per_run, "3", (n) => save({ op: "settings", key: "max_per_run", value: n }))),
        row(t("dot.dot_id"), t("dot.dot_id_hint"),
          textField(s.dot_id, t("dot.none"), (v) => save({ op: "settings", key: "dot_id", value: v })))),
      h("div", { class: "card feat-group" },
        h("h3", { class: "muted" }, t("dot.group_authors")),
        h("p", { class: "muted", style: { margin: "6px 0 0", fontSize: "12.5px" } }, t("dot.authors_hint")),
        AUTHOR_FEATURES.filter((k) => cfg.forms[k]).map((key) => h("div", { class: "stack", style: { gap: "8px", padding: "10px 0", borderTop: "1px solid var(--border)" } },
          h("b", {}, t(LABEL[key])),
          h("div", { class: "author-row" }, h("span", { class: "muted" }, t("dot.ignore_authors")),
            chipList((cfg.forms[key].default_ignore || []).map(String), "dependabot[bot]",
              (v) => save({ op: "default_authors", feature: key, key: "ignore_authors", value: v }))),
          h("div", { class: "author-row" }, h("span", { class: "muted" }, t("dot.only_authors")),
            chipList((cfg.forms[key].default_only || []).map(String), "login",
              (v) => save({ op: "default_authors", feature: key, key: "only_authors", value: v })))))),
      h("div", { class: "card feat-group" },
        h("h3", { class: "muted" }, t("dot.group_trust")),
        h("p", { class: "muted", style: { margin: "6px 0 0", fontSize: "12.5px" } },
          t("dot.trust_hint"), " ", h("a", { href: "#repos/repos.yaml" }, t("repos.open_repos_yaml"))),
        row(t("dot.trust_host"), t("dot.trust_host_hint"), h("span", { class: "badge" }, trust.host || "open")),
        row(t("dot.region"), t("dot.region_hint"), h("span", {}, s.region || trust.region || "—")),
        row(t("dot.allow"), t("dot.allow_hint"), h("span", { class: "muted" },
          t("dot.allow_values", [list(allow.providers), list(allow.models), list(allow.executors)]))),
        row(t("dot.zdr"), t("dot.zdr_hint"), h("span", { class: "muted" }, list(trust.zdr))),
        row(t("dot.self_hosted"), t("dot.self_hosted_hint"), h("span", { class: "muted" }, list(trust.self_hosted))),
        row(t("dot.executors"), t("dot.executors_hint"), execs.length
          ? h("span", { class: "muted mono" }, execs.map(([name, url]) => `${name} → ${url}`).join("  ·  "))
          : h("span", { class: "muted" }, "—"))),
    );
  }

  async function load() {
    try {
      cfg = await getJSON("/api/repos/config");
      paint();
    } catch (err) {
      mount(el, h("div", { class: "empty" }, t("repos.repos_yaml_cannot_be_read"), " ", err.body?.error || err.message));
    }
  }
  load();
  return el;
}
