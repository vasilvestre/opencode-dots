import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { confirmDialog } from "../modal.js";
import { toast } from "../toast.js";
import { openRulesDialog, ruleSummary } from "../rules-dialog.js";
import { ago, when } from "../format.js";
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

// One row per repository, one checkbox per thing the dot does on it. A click
// edits repos.yaml at once (the file is rewritten without its comments, a backup is stamped) and the
// generated files follow. The rules, the pull requests already seen and the
// Tower warning stay in the row's details.
export function mountRepos(el) {
  const state = { cfg: null, clones: null, rules: new Map(), filter: "", open: new Set(), error: "" };
  let chain = Promise.resolve(); // one write at a time, in click order

  const search = h("input", {
    type: "search",
    placeholder: t("common.filter_repositories"),
    "aria-label": t("common.filter_repositories"),
    oninput: () => { state.filter = search.value.trim().toLowerCase(); render(); },
  });
  // "with X" / "without X": which repositories have a feature on or off.
  const by = h("select", { "aria-label": t("repos.filter_by_feature"), onchange: () => { state.by = by.value; render(); } },
    h("option", { value: "" }, t("repos.any_feature")));
  const count = h("span", { class: "muted" });
  const body = h("div", {});
  const claims = h("div", {});
  const workers = h("div", {});
  const addInput = h("input", {
    type: "text",
    placeholder: "owner/name",
    "aria-label": t("repos.repository_to_add"),
    class: "mono",
    onkeydown: (ev) => { if (ev.key === "Enter") { ev.preventDefault(); addRepo(); } },
  });

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, t("repos.repositories")),
        h("p", { class: "muted" },
          t("repos.what_the_dot_does_on"),
          h("a", { href: "#config/repos.yaml" }, "repos.yaml"),
          t("repos.as_you_click_and_the")),
      ),
      h("div", { class: "row" }, by, search),
    ),
    h("div", { class: "section-head" }, count, h("span", { class: "spacer" }),
      addInput, h("button", { class: "btn sm", onclick: addRepo }, t("repos.add_repository"))),
    claims,
    workers,
    body,
  ));
  loadClaims();
  loadWorkers();

  // A claim is a veto: the dot does not push to a branch a person holds (ADR 0026).
  // The list is short and changes slowly, so one read on mount is enough.
  async function loadClaims() {
    let list = [];
    try { list = await getJSON("/api/claims"); } catch { return; }
    if (!list.length) return;
    mount(claims, h("div", { class: "card" },
      h("div", { class: "card-head" },
        h("h3", {}, t("repos.claims")),
        h("span", { class: "badge warn" }, `${list.length}`),
      ),
      h("p", { class: "muted" }, t("repos.the_dot_does_not_push")),
      h("div", { class: "list" }, list.map((c) =>
        h("div", { class: "item row between" },
          h("div", {},
            h("span", { class: "mono" }, c.repo),
            " ",
            h("span", { class: "mono" }, c.branch),
            c.title ? h("span", { class: "muted" }, ` — ${c.title}`) : null,
          ),
          h("div", { class: "muted" },
            c.machine, t("repos.held"), ago(c.held_since),
            c.expires_at ? t("repos.expires", [when(c.expires_at)]) : ""),
        ))),
    ));
  }

  // Workers (ADR 0042): machines that register with this instance. Presence only, the
  // instance sends them nothing. The card is absent until one registers.
  async function loadWorkers() {
    let list = [];
    try { list = await getJSON("/api/workers"); } catch { return; }
    if (!list.length) return;
    const live = list.filter((w) => w.live).length;
    mount(workers, h("div", { class: "card" },
      h("div", { class: "card-head" },
        h("h3", {}, t("repos.workers")),
        h("span", { class: `badge ${live ? "ok" : "warn"}` }, `${live} live`),
        list.length > live ? h("span", { class: "badge" }, t("repos.expired", [list.length - live])) : null,
      ),
      h("p", { class: "muted" }, t("repos.machines_registered_with_this_instance")),
      h("div", { class: "list" }, list.map((w) =>
        h("div", { class: "item row between" },
          h("div", {},
            h("span", { class: "mono" }, w.name),
            " ",
            h("span", { class: `badge ${w.live ? "ok" : "warn"}` }, w.live ? "live" : "expired"),
            capSummary(w.capabilities) ? h("span", { class: "muted" }, ` — ${capSummary(w.capabilities)}`) : null,
          ),
          h("div", { class: "muted" },
            w.machine, t("repos.registered"), ago(w.registered_at), t("repos.last_seen"), ago(w.renewed_at),
            w.live ? t("repos.expires", [when(w.expires_at)]) : ""),
        ))),
    ));
  }

  function capSummary(caps) {
    return Object.keys(caps || {}).join(", ");
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
        state.open.add(name);
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
    if (ok) save({ op: "remove", repo: row.name }, { done: () => { state.open.delete(row.name); toast(t("repos.removed", [row.name]), { tone: "ok" }); } });
  }

  // --- rendering --------------------------------------------------------------

  // summaries is one line per rule of a feature, in words.
  function summaries(row, key) {
    const form = state.cfg.forms[key];
    const rules = row.features[key].rules || [];
    return rules.map((r) => ruleSummary(r, form));
  }

  function labelOf(key) {
    return FEATURES.find((f) => f[0] === key)?.[1] || key;
  }

  function render() {
    if (state.error) {
      mount(body, h("div", { class: "issues bad" },
        h("div", { class: "head" }, t("repos.repos_yaml_cannot_be_read")),
        h("ul", {}, state.error.split("\n").map((l) => h("li", {}, l))),
        h("p", { style: { padding: "0 12px 10px" } },
          h("a", { href: "#config/repos.yaml" }, t("repos.open_repos_yaml")), t("repos.to_fix_it"))));
      count.textContent = "";
      return;
    }
    if (!state.cfg) return;
    if (by.options.length === 1 && state.cfg.repos[0]) {
      for (const key of Object.keys(state.cfg.repos[0].features)) {
        by.append(h("option", { value: `+${key}` }, t("repos.with", [labelOf(key)])), h("option", { value: `-${key}` }, t("repos.without", [labelOf(key)])));
      }
    }
    const rows = state.cfg.repos.filter((r) => (!state.filter || r.name.toLowerCase().includes(state.filter))
      && (!state.by || !!r.features[state.by.slice(1)]?.on === (state.by[0] === "+")));
    count.textContent = t("repos.of_repositories", [rows.length, state.cfg.repos.length]);
    if (!rows.length) {
      mount(body, h("div", { class: "empty" }, state.cfg.repos.length ? t("common.nothing_matches") : t("repos.no_repository_yet_add_one")));
      return;
    }
    const head = h("tr", {},
      h("th", {}, t("common.repository")),
      FEATURES.map(([, label, title]) => h("th", { class: "cell-check", title }, label)),
      h("th", {}, t("repos.agent")),
      h("th", { class: "cell-check", title: t("repos.pull_requests_the_watcher_already") }, t("repos.seen")),
      h("th", {}, ""),
    );
    mount(body, h("div", { class: "table-wrap" },
      h("table", { class: "repo-table" }, h("thead", {}, head), h("tbody", {}, rows.map(rowEls)))));
  }

  function rowEls(row) {
    const open = state.open.has(row.name);
    const rule = state.rules.get(row.name);
    const tr = h("tr", { class: open ? "open" : "" },
      h("td", {},
        h("b", {}, row.name),
        row.trust && row.trust !== "open"
          ? h("span", { class: "badge warn", title: t("repos.adr_what_this_repository_may"), style: { marginLeft: "6px" } }, row.trust)
          : null,
        row.use?.length ? h("span", { class: "chips", style: { display: "inline-flex", marginLeft: "6px" } },
          row.use.map((u) => h("span", { class: "chip", title: t("repos.profile_in_repos_yaml") }, u))) : null),
      FEATURES.map(([key]) => featureCell(row, key)),
      h("td", { "data-cell": `${row.name}|provider` }, providerSelect(row)),
      h("td", { class: "cell-check num muted" }, rule?.seen.length ? String(rule.seen.length) : "—"),
      h("td", { class: "actions" },
        h("button", {
          class: "btn ghost sm",
          "aria-expanded": String(open),
          title: open ? t("repos.hide_the_rules_and_details") : t("repos.show_the_rules_and_details"),
          onclick: () => { open ? state.open.delete(row.name) : state.open.add(row.name); render(); },
        }, open ? t("repos.hide") : t("common.details"))),
    );
    return open ? [tr, detailRow(row, rule)] : [tr];
  }

  function featureCell(row, key) {
    if (key === "auto_merge") return autoMergeCell(row);
    const f = row.features[key];
    // sync needs a clone on this machine; a server has none. Clone from the Projects card on Home.
    if (key === "sync" && !f.on && !state.clones?.has(row.name)) {
      return h("td", { class: "cell-check muted", title: t("repos.sync_needs_a_clone") }, "–");
    }
    const box = h("input", {
      type: "checkbox",
      checked: f.on,
      "aria-label": `${labelOf(key)} on ${row.name}`,
      title: f.source === "profile" ? t("repos.on_through_the_profile", [row.use?.join(", ")]) : "",
    });
    // The rules button sits outside the flow, so a custom cell stays aligned
    // with the others.
    const rulesBtn = f.on && state.cfg.forms[key]
      ? h("button", {
        class: `rules-btn${f.custom ? " custom" : ""}`,
        title: f.custom ? t("repos.runs_on_click_to_edit", [summaries(row, key).join(" / ")]) : t("repos.choose_when_it_runs"),
        "aria-label": t("repos.edit_when_runs_on", [labelOf(key), row.name]),
        onclick: () => editRules(row, key),
      }, "⚙")
      : null;
    const td = h("td", { class: "cell-check", "data-cell": `${row.name}|${key}` }, h("span", { class: "cb" }, box, rulesBtn));
    box.addEventListener("change", () => toggle(row, key, box.checked, td));
    return td;
  }

  function autoMergeCell(row) {
    const tend = row.features.tend;
    const box = h("input", {
      type: "checkbox",
      checked: Boolean(tend.auto_merge),
      disabled: !tend.on,
      "aria-label": t("repos.auto_merge_on", [row.name]),
      title: tend.on ? t("repos.merge_when_review_pr_says") : t("repos.tick_tend_first"),
    });
    const td = h("td", { class: "cell-check", "data-cell": `${row.name}|auto_merge` }, h("span", { class: "cb" }, box));
    box.addEventListener("change", () => save({ repo: row.name, key: "auto_merge", value: box.checked }, { cell: td }));
    return td;
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
      state.cfg.providers.map((p) => h("option", { value: p }, p)));
    sel.value = row.provider_source === "repo" ? row.provider : "";
    sel.addEventListener("change", () => save({ repo: row.name, key: "provider", value: sel.value || null }, { cell: sel.closest("td") }));
    return sel;
  }

  function detailRow(row, rule) {
    const watched = FEATURES.map(([key]) => key)
      .filter((key) => state.cfg.forms[key] && row.features[key]?.on)
      .map((key) => [key, summaries(row, key)]);
    return h("tr", { class: "detail" },
      h("td", { colspan: String(FEATURES.length + 4) },
        h("div", { class: "stack", style: { gap: "10px" } },
          h("div", { class: "row" },
            h("a", { href: `https://github.com/${ghPath(row.name)}`, target: "_blank", rel: "noreferrer" }, t("common.open_on_github")),
            row.checkout ? h("span", { class: "muted mono" }, t("repos.checkout", [row.checkout])) : h("span", { class: "muted" }, t("repos.no_local_checkout")),
            h("span", { class: "spacer" }),
            h("button", { class: "btn danger sm", onclick: () => removeRepo(row) }, t("repos.remove_repository")),
          ),
          rule?.tower_ignore ? h("div", { class: "warning" },
            rule.tower_ignore === "missing"
              ? t("repos.tower_indexes_this_repository_and")
              : t("repos.tower_indexes_this_repository_and_2")) : null,
          watched.length ? h("div", { class: "stack", style: { gap: "6px" } },
            h("h3", { class: "muted" }, t("repos.when_the_watcher_acts")),
            watched.map(([key, lines]) => h("div", { class: "row", style: { fontSize: "12.5px", alignItems: "baseline" } },
              h("b", { style: { minWidth: "70px" } }, labelOf(key)),
              h("span", { class: "muted" }, lines.join("  +  ")),
              h("button", { class: "btn ghost sm", onclick: () => editRules(row, key) }, t("repos.edit"))),
            )) : h("div", { class: "muted" }, t("repos.no_watcher_rule_this_repository")),
          rule?.seen.length ? h("div", { class: "chips" },
            rule.seen.slice().reverse().map((pr) => h("a", {
              class: "chip",
              href: `https://github.com/${ghPath(row.name)}/pull/${encodeURIComponent(pr)}`,
              target: "_blank",
              rel: "noreferrer",
            }, `#${pr}`))) : null,
        )));
  }

  // --- loading ----------------------------------------------------------------

  async function reload() {
    try {
      state.cfg = await getJSON("/api/repos/config");
      // Which repositories have a clone here: sync fast-forwards one, so it is offered only for those.
      // A visitor cannot read the list and sees no sync box either (it is an owner setting).
      state.clones = new Set((await getJSON("/api/checkouts").catch(() => ({ projects: [] }))).projects
        .filter((p) => p.state === "found").map((p) => p.repo));
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

  return { destroy() { clearInterval(timer); } };
}
