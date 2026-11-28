import { h, mount } from "../dom.js";
import { getJSON, postJSON } from "../api.js";
import { confirmDialog } from "../modal.js";
import { toast } from "../toast.js";
import { openRulesDialog, ruleSummary } from "../rules-dialog.js";
import { filesPanel } from "../files-panel.js";
import { t } from "../i18n.js";

const FEATURES = [
  ["review", t("common.review"), t("repos.review_pr_on_pull_requests")],
  ["reply", t("repos.reply"), t("repos.reply_pr_on_review_comments")],
  ["fix_issue", t("repos.fix_issue"), t("repos.fix_issue_on_new_issues")],
  ["fix_ci", t("repos.fix_ci"), t("repos.fix_ci_on_failed_checks")],
  ["tend", t("repos.tend"), t("repos.the_daily_tend_pass")],
  ["auto_merge", t("common.auto_merge"), t("repos.tend_merges_a_pull_request")],
  ["sync", t("repos.sync"), t("repos.keep_the_local_checkout_fast")],
];

// ghPath encodes each segment of owner/name before it goes into a URL.
const ghPath = (name) => name.split("/").map(encodeURIComponent).join("/");

// Repositories: a list on the left with each one's state, the selected repository's settings on
// the right, grouped by what the dot does (pull requests, issues and CI, maintenance). A switch
// edits repos.yaml at once (rewritten without its comments, a backup is stamped) and the generated
// files follow. The raw files sit under "Advanced".
const GROUPS = [
  ["repos.group_pr", ["review", "reply"]],
  ["repos.group_work", ["fix_issue", "fix_ci"]],
  ["repos.group_maint", ["tend", "auto_merge", "sync"]],
];

export function mountRepos(el, arg) {
  const state = { cfg: null, clones: null, projects: new Map(), rules: new Map(), filter: "", sel: "", error: "" };
  let chain = Promise.resolve(); // one write at a time, in click order

  const search = h("input", {
    type: "search",
    placeholder: t("common.filter_repositories"),
    "aria-label": t("common.filter_repositories"),
    oninput: () => { state.filter = search.value.trim().toLowerCase(); renderList(); },
  });
  const listEl = h("div", { class: "side-list" });
  const defaultAgent = h("div", { class: "row muted", style: { fontSize: "13px" } });
  const detail = h("div", { class: "stack" });
  const body = h("div", {});
  const files = filesPanel(arg);
  const advanced = h("details", { open: Boolean(arg) },
    h("summary", {}, t("repos.advanced")), files.el);
  advanced.addEventListener("toggle", () => { if (advanced.open) files.load(); });
  if (arg) files.load();
  const addInput = h("input", {
    type: "text",
    placeholder: "owner/name",
    "aria-label": t("repos.repository_to_add"),
    class: "mono",
    onkeydown: (ev) => { if (ev.key === "Enter") { ev.preventDefault(); addRepo(); } },
  });

  mount(el, h("div", { class: "stack" },
    h("div", {},
      h("h1", {}, t("repos.repositories")),
      h("p", { class: "muted" },
        t("repos.what_the_dot_does_on"),
        h("a", { href: "#repos/repos.yaml", onclick: openAdvanced }, "repos.yaml"),
        t("repos.as_you_click_and_the"))),
    defaultAgent,
    body,
    advanced,
  ));

  function openAdvanced(ev) {
    ev.preventDefault();
    advanced.open = true;
    advanced.scrollIntoView({ block: "nearest" });
  }

  // --- writes ---------------------------------------------------------------

  async function patch(payload) {
    const res = await fetch("/api/repos/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const text = await res.text();
    let data = {};
    try { data = JSON.parse(text); } catch { data = { error: text }; }
    if (!res.ok) throw Object.assign(new Error(data.error || res.statusText), { status: res.status });
    return data;
  }

  // save runs one change after the previous finished. A failed change shows its
  // reason and repaints from the file, so the page never claims what the file
  // does not say.
  function save(payload, { cell, done } = {}) {
    cell?.classList.add("saving");
    const run = chain.then(async () => {
      try {
        const res = await patch(payload);
        state.cfg = res.config;
        cell?.classList.remove("saving");
        done?.(res);
        render();
        flash(payload);
      } catch (err) {
        cell?.classList.remove("saving");
        toast(err.message, { tone: "err", timeout: 9000 });
        await reload();
      }
    });
    chain = run.catch(() => {});
    return run;
  }

  function flash(payload) {
    const key = `${payload.repo}|${payload.key || payload.op}`;
    const cell = [...el.querySelectorAll("[data-cell]")].find((c) => c.dataset.cell === key);
    if (!cell) return;
    cell.classList.add("saved");
    setTimeout(() => cell.classList.remove("saved"), 1200);
  }

  async function toggle(row, key, on, cell) {
    const f = row.features[key];
    if (!on && f.custom) {
      const ok = await confirmDialog({
        title: t("repos.switch_off_on", [labelOf(key), row.name]),
        message: t("repos.it_has_its_own_rules", [summaries(row, key).join(" / ")]),
        confirm: t("repos.switch_off"),
        danger: true,
      });
      if (!ok) { render(); return; }
    }
    save({ repo: row.name, key, value: on }, { cell });
  }

  async function addRepo() {
    const name = addInput.value.trim();
    if (!name) return;
    if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(name)) {
      toast(t("repos.use_the_owner_name_form"), { tone: "err" });
      return;
    }
    await save({ op: "add", repo: name }, {
      done: () => {
        addInput.value = "";
        state.sel = name;
        state.filter = "";
        search.value = "";
        toast(t("repos.added_with_everything_off_tick", [name]), { tone: "ok" });
      },
    });
  }

  async function removeRepo(row) {
    const ok = await confirmDialog({
      title: t("repos.remove", [row.name]),
      message: t("repos.the_repository_leaves_repos_yaml"),
      confirm: t("repos.remove_2"),
      danger: true,
    });
    if (ok) save({ op: "remove", repo: row.name }, { done: () => { state.sel = ""; toast(t("repos.removed", [row.name]), { tone: "ok" }); } });
  }

  // --- rendering --------------------------------------------------------------

  // summaries is one line per rule of a feature, in words.
  function summaries(row, key) {
    const form = state.cfg.forms[key];
    const rules = row.features[key].rules || [];
    return rules.map((r) => ruleSummary(r, form));
  }

  const agentLabel = (name) => (name === "any" ? t("repos.provider_any") : name);
  const labelOf = (key) => FEATURES.find((f) => f[0] === key)?.[1] || key;
  const hintOf = (key) => FEATURES.find((f) => f[0] === key)?.[2] || "";
  const activeCount = (row) => Object.values(row.features).filter((f) => f.on).length + (row.features.tend?.auto_merge ? 1 : 0);
  const missingClone = (row) => { const p = state.projects.get(row.name); return Boolean(p) && p.state !== "found" && !row.checkout; };

  function render() {
    if (state.error) {
      mount(body, h("div", { class: "issues bad" },
        h("div", { class: "head" }, t("repos.repos_yaml_cannot_be_read")),
        h("ul", {}, state.error.split("\n").map((l) => h("li", {}, l))),
        h("p", { style: { padding: "0 12px 10px" } },
          h("a", { href: "#repos/repos.yaml", onclick: openAdvanced }, t("repos.open_repos_yaml")), t("repos.to_fix_it"))));
      return;
    }
    if (!state.cfg) return;
    if (!state.cfg.repos.some((r) => r.name === state.sel)) state.sel = state.cfg.repos[0]?.name || "";
    if (!body.querySelector(".split")) {
      mount(body, h("div", { class: "split" },
        h("aside", { class: "side" }, search, listEl,
          h("div", { class: "row", style: { gap: "6px" } }, addInput,
            h("button", { class: "btn sm", onclick: addRepo }, t("repos.add_repository")))),
        detail));
    }
    renderDefaultAgent();
    renderList();
    renderDetail();
  }

  // The agent CLI a repository uses when it names none: defaults.provider of repos.yaml.
  function renderDefaultAgent() {
    const sel = h("select", { "aria-label": t("repos.default_agent") },
      state.cfg.default_providers.map((p) => h("option", { value: p, selected: p === (state.cfg.default_provider || "opencode") }, agentLabel(p))));
    sel.addEventListener("change", () => save({ op: "default_provider", value: sel.value === "opencode" ? "" : sel.value }));
    mount(defaultAgent, t("repos.default_agent"), sel, h("span", {}, t("repos.default_agent_hint")));
  }

  function renderList() {
    if (!state.cfg) return;
    const rows = state.cfg.repos.filter((r) => !state.filter || r.name.toLowerCase().includes(state.filter));
    // With one owner on the whole list, the owner is noise: the header of the detail still has it.
    const oneOwner = new Set(state.cfg.repos.map((r) => r.name.split("/")[0])).size === 1;
    const short = (name) => (oneOwner ? name.split("/")[1] : name);
    mount(listEl, rows.length ? rows.map((r) => {
      const n = activeCount(r);
      const tone = missingClone(r) ? "warn" : n ? "ok" : "off";
      return h("button", {
        class: "side-item",
        "aria-current": r.name === state.sel ? "true" : null,
        onclick: () => { state.sel = r.name; renderList(); renderDetail(); },
      },
      h("b", {}, h("i", { class: `dot-state ${tone}`, "aria-hidden": "true" }), short(r.name),
        r.trust && r.trust !== "open" ? h("span", { class: "badge warn", style: { marginLeft: "6px" } }, r.trust) : null),
      h("span", {}, n ? t("repos.active_count", [n]) : t("repos.nothing_active"), " · ", agentLabel(r.provider || "opencode"),
        missingClone(r) ? t("repos.not_cloned_suffix") : ""));
    }) : h("div", { class: "empty" }, state.cfg.repos.length ? t("common.nothing_matches") : t("repos.no_repository_yet_add_one")));
  }

  function renderDetail() {
    const row = state.cfg?.repos.find((r) => r.name === state.sel);
    if (!row) { mount(detail, h("div", { class: "empty" }, t("repos.no_repository_yet_add_one"))); return; }
    const rule = state.rules.get(row.name);
    mount(detail,
      h("div", { class: "row between", style: { alignItems: "flex-start" } },
        h("div", {},
          h("h2", {}, row.name),
          h("div", { class: "muted", style: { fontSize: "12.5px" } },
            h("a", { href: `https://github.com/${ghPath(row.name)}`, target: "_blank", rel: "noreferrer" }, t("common.open_on_github")),
            rule?.seen.length ? ` · ${t("repos.seen_prs", [rule.seen.length])}` : "",
            row.use?.length ? ` · ${row.use.join(", ")}` : "")),
        h("div", { class: "row" }, cloneState(row), h("span", { "data-cell": `${row.name}|provider` }, providerSelect(row)))),
      rule?.tower_ignore ? h("div", { class: "warning" },
        rule.tower_ignore === "missing" ? t("repos.tower_indexes_this_repository_and") : t("repos.tower_indexes_this_repository_and_2")) : null,
      GROUPS.map(([title, keys]) => h("div", { class: "card feat-group" },
        h("h3", { class: "muted" }, t(title)),
        keys.map((key) => featureRow(row, key)))),
      sourcesCard(row),
      safetyCard(row),
      rule?.seen.length ? h("div", { class: "chips" },
        rule.seen.slice().reverse().map((pr) => h("a", {
          class: "chip", href: `https://github.com/${ghPath(row.name)}/pull/${encodeURIComponent(pr)}`, target: "_blank", rel: "noreferrer",
        }, `#${pr}`))) : null,
      h("div", { class: "row" }, h("span", { class: "spacer" }),
        h("button", { class: "btn danger sm", onclick: () => removeRepo(row) }, t("repos.remove_repository"))),
    );
  }

  // What the dot may do here and how it is held back. Each control writes its key at once, like
  // the switches; an empty choice removes the key, so the default applies again.
  const AUTONOMY = ["observe", "propose", "act", "auto"];
  const IDENTITY = ["off", "marker", "signed"];

  // below is a wide field (a command) that gets its own full-width line under the row.
  function safetyRow(row, key, label, hint, control, below) {
    return h("div", { class: `feat-row on${below ? " wrap" : ""}`, "data-cell": `${row.name}|${key}` },
      h("div", { class: "feat-text" }, h("b", {}, label), h("div", { class: "muted" }, hint)),
      control, below || null);
  }

  function choice(row, key, values, current, defaultLabel) {
    const sel = h("select", { "aria-label": `${key} on ${row.name}` },
      h("option", { value: "" }, defaultLabel),
      values.map((v) => h("option", { value: v, selected: v === current }, t(`repos.${key}_${v}`))));
    sel.addEventListener("change", () => save({ repo: row.name, key, value: sel.value || null }, { cell: sel.closest(".feat-row") }));
    return sel;
  }

  function textField(row, key, value, placeholder, { type = "text", parse = (v) => v || null } = {}) {
    const input = h("input", { type, class: type === "number" ? "" : "mono", value: value ?? "", placeholder, "aria-label": `${key} on ${row.name}`,
      style: { width: type === "number" ? "110px" : "260px" }, min: type === "number" ? "0" : null, step: type === "number" ? "0.01" : null });
    const commit = () => { if (input.value !== String(value ?? "")) save({ repo: row.name, key, value: parse(input.value.trim()) }, { cell: input.closest(".feat-row") }); };
    input.addEventListener("change", commit);
    input.addEventListener("keydown", (ev) => { if (ev.key === "Enter") input.blur(); });
    return input;
  }

  // A list of project names, one chip each with a way to remove it, and a field to add one. Each
  // change writes the whole list; one project is stored as a plain value, several as a list.
  function projectList(row, key, values, placeholder, normalize) {
    const save_ = (list) => save({ repo: row.name, key, value: list }, { cell: wrap.closest(".feat-row") });
    const input = h("input", { type: "text", class: "mono", placeholder, "aria-label": `${key} on ${row.name}`, style: { width: "130px" } });
    const add = () => {
      const name = normalize(input.value.trim());
      if (!name) return;
      if (values.includes(name)) { toast(t("repos.source_already", [name]), { tone: "err" }); return; }
      save_([...values, name]);
    };
    input.addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); add(); } });
    const wrap = h("div", { class: "chips", style: { justifyContent: "flex-end", alignItems: "center" } },
      values.map((name) => h("span", { class: "chip" }, name,
        h("button", { class: "chip-x", title: t("repos.source_remove", [name]), "aria-label": t("repos.source_remove", [name]),
          onclick: () => save_(values.filter((v) => v !== name)) }, "×"))),
      input,
      h("button", { class: "btn sm", onclick: add }, t("repos.source_add")));
    return wrap;
  }

  // A command is read as written: several lines, wrapped, saved when the field loses focus.
  function commandField(row, key, value, placeholder) {
    const area = h("textarea", { class: "mono cmd", rows: Math.min(6, Math.max(2, (value || "").split("\n").length + Math.floor((value || "").length / 70))),
      spellcheck: false, placeholder, "aria-label": `${key} on ${row.name}` }, value || "");
    area.addEventListener("change", () => save({ repo: row.name, key, value: area.value.trim() || null }, { cell: area.closest(".feat-row") }));
    return area;
  }

  function switchFor(row, key, on) {
    const box = h("input", { type: "checkbox", role: "switch", class: "switch", checked: on, "aria-label": `${key} on ${row.name}` });
    box.addEventListener("change", () => save({ repo: row.name, key, value: box.checked }, { cell: box.closest(".feat-row") }));
    return box;
  }

  function safetyCard(row) {
    const sb = row.sandbox;
    return h("div", { class: "card feat-group" },
      h("h3", { class: "muted" }, t("repos.group_safety")),
      safetyRow(row, "autonomy", t("repos.autonomy"), t(`repos.autonomy_${row.autonomy || "default"}_hint`),
        choice(row, "autonomy", AUTONOMY, row.autonomy, t("repos.autonomy_default"))),
      safetyRow(row, "circuit_breaker", t("repos.circuit_breaker"), t("repos.circuit_breaker_hint"),
        switchFor(row, "circuit_breaker", Boolean(row.circuit_breaker))),
      safetyRow(row, "budget", t("repos.budget"), t("repos.budget_hint"),
        textField(row, "budget", row.budget_usd, t("repos.budget_none"), { type: "number", parse: (v) => (v === "" ? null : Number(v)) })),
      safetyRow(row, "identity", t("repos.identity"), t("repos.identity_hint"),
        choice(row, "identity", IDENTITY, row.identity, t("repos.identity_default"))),
      safetyRow(row, "sandbox", t("repos.sandbox"), sb?.detail ? `${t("repos.sandbox_hint")} (${sb.detail})` : t("repos.sandbox_hint"),
        switchFor(row, "sandbox", Boolean(sb)),
        sb ? commandField(row, "sandbox_command", sb.command, t("repos.sandbox_command")) : null),
      row.trust && row.trust !== "open" ? safetyRow(row, "trust", t("repos.trust"), t("repos.trust_hint"),
        h("span", { class: "badge warn" }, row.trust)) : null);
  }

  // Where work comes from. GitHub is the watcher: its events feed the four switches above, so the
  // row summarises them. Jira, Sentry and the advisories each name what to poll.
  function sourcesCard(row) {
    const github = ["review", "reply", "fix_issue", "fix_ci"].filter((k) => row.features[k]?.on).map(labelOf);
    return h("div", { class: "card feat-group" },
      h("h3", { class: "muted" }, t("repos.group_sources")),
      h("div", { class: "feat-row on" },
        h("div", { class: "feat-text" }, h("b", {}, "GitHub"),
          h("div", { class: "muted" }, github.length ? t("repos.source_github_on", [github.join(", ")]) : t("repos.source_github_off"))),
        h("span", { class: `badge ${github.length ? "ok" : ""}` }, github.length ? t("repos.source_active") : t("repos.source_inactive"))),
      safetyRow(row, "jira", "Jira", t("repos.source_jira_hint"),
        projectList(row, "jira", row.jira || [], "ABC", (v) => v.toUpperCase())),
      safetyRow(row, "sentry", "Sentry", t("repos.source_sentry_hint"),
        projectList(row, "sentry", row.sentry || [], "org/project", (v) => v.toLowerCase())),
      safetyRow(row, "advisories", t("repos.source_advisories"), t("repos.source_advisories_hint"),
        switchFor(row, "advisories", Boolean(row.advisories))));
  }

  function cloneState(row) {
    if (row.checkout) return h("span", { class: "badge ok", title: row.checkout }, t("repos.cloned"));
    return h("span", { class: "badge warn" }, t("repos.not_cloned"));
  }

  // One line per thing the dot does: a switch, what it is, when it runs, and the next step when
  // something is missing (a clone, the tend pass) instead of a silent gray box.
  function featureRow(row, key) {
    const tend = row.features.tend;
    const f = row.features[key] || {}; // auto_merge is a tend setting, not a feature of its own
    let blocked = "";
    let action = null;
    if (key === "auto_merge" && !tend.on) blocked = t("repos.tick_tend_first");
    if (key === "sync" && !f.on && !state.clones?.has(row.name)) {
      blocked = t("repos.sync_needs_a_clone");
      action = cloneBtn(row);
    }
    const on = key === "auto_merge" ? Boolean(tend.auto_merge) : f.on;
    const box = h("input", {
      type: "checkbox", role: "switch", class: "switch", checked: on, disabled: Boolean(blocked),
      "aria-label": `${labelOf(key)} on ${row.name}`,
      title: f.source === "profile" ? t("repos.on_through_the_profile", [row.use?.join(", ")]) : "",
    });
    const hasRules = on && state.cfg.forms[key];
    const when_ = hasRules ? (f.custom ? summaries(row, key).join(" / ") : t("repos.default_rules")) : "";
    const line = h("div", { class: `feat-row${on ? " on" : ""}`, "data-cell": `${row.name}|${key}` },
      box,
      h("div", { class: "feat-text" },
        h("b", {}, labelOf(key)),
        h("div", { class: "muted" }, blocked || (when_ ? t("repos.runs_on", [when_]) : hintOf(key)))),
      action,
      hasRules ? h("button", {
        class: `btn ghost sm${f.custom ? " custom" : ""}`,
        title: t("repos.choose_when_it_runs"),
        "aria-label": t("repos.edit_when_runs_on", [labelOf(key), row.name]),
        onclick: () => editRules(row, key),
      }, t("repos.rules")) : null);
    box.addEventListener("change", () => {
      if (key === "auto_merge") save({ repo: row.name, key, value: box.checked }, { cell: line });
      else toggle(row, key, box.checked, line);
    });
    return line;
  }

  // A repository with no clone here can be cloned where the Projects card on Home would put it.
  function cloneBtn(row) {
    const p = state.projects.get(row.name);
    if (row.checkout || !p?.suggested || p.state === "found") return null;
    return h("button", {
      class: "btn sm",
      title: p.suggested,
      onclick: async () => {
        try {
          const r = await postJSON("/api/checkouts/clone", { repo: row.name });
          toast(t("home.cloning_into", [row.name, r.target]), { tone: "ok" });
          setTimeout(reload, 1500);
        } catch (err) {
          toast(t("home.cannot_clone", [err.body?.error || err.message]), { tone: "err", timeout: 9000 });
        }
      },
    }, p.state === "failed" ? t("common.retry") : t("home.clone"));
  }

  function editRules(row, key) {
    const f = row.features[key];
    openRulesDialog({
      repo: row.name,
      featureLabel: labelOf(key),
      form: state.cfg.forms[key],
      rules: f.rules || [],
      onSave: async (rules) => {
        const write = chain.then(async () => {
          const current = state.cfg.repos.find((r) => r.name === row.name)?.features[key].rules;
          if (JSON.stringify(rules) === JSON.stringify(current)) return; // nothing changed
          const res = await patch({ repo: row.name, key: "rules", feature: key, value: rules });
          state.cfg = res.config;
          render();
          flash({ repo: row.name, key });
          toast(t("repos.saved_the_rules_of_on", [labelOf(key), row.name]), { tone: "ok" });
        });
        chain = write.catch(() => {});
        await write;
      },
    });
  }

  function providerSelect(row) {
    const sel = h("select", { "aria-label": t("repos.agent_cli_for", [row.name]), class: "provider" },
      h("option", { value: "" }, row.provider_source === "default" ? t("common.default", [row.provider]) : t("repos.default_opencode")),
      state.cfg.providers.filter((p) => p !== "any" || row.executor).map((p) => h("option", { value: p }, agentLabel(p))));
    sel.value = row.provider_source === "repo" ? row.provider : "";
    sel.addEventListener("change", () => save({ repo: row.name, key: "provider", value: sel.value || null }, { cell: sel.parentElement }));
    return sel;
  }

  // --- loading ----------------------------------------------------------------

  async function reload() {
    try {
      state.cfg = await getJSON("/api/repos/config");
      // Which repositories have a clone here: sync fast-forwards one, so it is offered only for those.
      // A visitor cannot read the list and sees no sync box either (it is an owner setting).
      const projects = (await getJSON("/api/checkouts").catch(() => ({ projects: [] }))).projects;
      state.projects = new Map(projects.map((p) => [p.repo, p]));
      state.clones = new Set(projects.filter((p) => p.state === "found").map((p) => p.repo));
      state.error = "";
    } catch (err) {
      state.cfg = null;
      state.error = err.body?.error || err.message;
    }
    render();
  }

  async function loadRules() {
    try {
      const rules = await getJSON("/api/repos");
      state.rules = new Map(rules.map((r) => [r.repo, r]));
    } catch { /* the details stay empty */ }
    render();
  }

  reload();
  loadRules();
  const timer = setInterval(loadRules, 30000);

  return { destroy() { clearInterval(timer); files.destroy(); } };
}
