import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { confirmDialog } from "../modal.js";
import { toast } from "../toast.js";

const FEATURES = [
  ["review", "Review", "review-pr on pull requests"],
  ["reply", "Reply", "reply-pr on review comments of my own pull requests"],
  ["fix_issue", "Fix issue", "fix-issue on new issues, or on one label"],
  ["fix_ci", "Fix CI", "fix-ci on failed checks of my own pull requests"],
  ["tend", "Tend", "the daily tend pass"],
  ["sync", "Sync", "keep the local checkout fast-forwarded"],
];

// ghPath encodes each segment of owner/name before it goes into a URL.
const ghPath = (name) => name.split("/").map(encodeURIComponent).join("/");

// One row per repository, one checkbox per thing the dot does on it. A click
// edits repos.yaml at once (comments are kept, a backup is stamped) and the
// generated files follow. The rules, the pull requests already seen and the
// Tower warning stay in the row's details.
export function mountRepos(el) {
  const state = { cfg: null, rules: new Map(), filter: "", open: new Set(), error: "" };
  let chain = Promise.resolve(); // one write at a time, in click order

  const search = h("input", {
    type: "search",
    placeholder: "Filter repositories",
    "aria-label": "Filter repositories",
    oninput: () => { state.filter = search.value.trim().toLowerCase(); render(); },
  });
  const count = h("span", { class: "muted" });
  const body = h("div", {});
  const addInput = h("input", {
    type: "text",
    placeholder: "owner/name",
    "aria-label": "Repository to add",
    class: "mono",
    onkeydown: (ev) => { if (ev.key === "Enter") { ev.preventDefault(); addRepo(); } },
  });

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, "Repositories"),
        h("p", { class: "muted" },
          "What the dot does on each repository. A change is saved to ",
          h("a", { href: "#config/repos.yaml" }, "repos.yaml"),
          " as you click, and the watcher files are regenerated."),
      ),
      h("div", { class: "row" }, search),
    ),
    h("div", { class: "section-head" }, count, h("span", { class: "spacer" }),
      addInput, h("button", { class: "btn sm", onclick: addRepo }, "Add repository")),
    body,
  ));

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
        title: `Switch off ${labelOf(key)} on ${row.name}?`,
        message: `It has its own rules (${describe(f.detail)}). They are dropped from repos.yaml; the previous file is kept as a backup.`,
        confirm: "Switch off",
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
      toast("Use the owner/name form, for example acme/widgets.", { tone: "err" });
      return;
    }
    await save({ op: "add", repo: name }, {
      done: () => {
        addInput.value = "";
        state.open.add(name);
        state.filter = "";
        search.value = "";
        toast(`${name} added with everything off. Tick what the dot should do.`, { tone: "ok" });
      },
    });
  }

  async function removeRepo(row) {
    const ok = await confirmDialog({
      title: `Remove ${row.name}?`,
      message: "The repository leaves repos.yaml and the watcher stops looking at it. The previous file is kept as a backup.",
      confirm: "Remove",
      danger: true,
    });
    if (ok) save({ op: "remove", repo: row.name }, { done: () => { state.open.delete(row.name); toast(`${row.name} removed.`, { tone: "ok" }); } });
  }

  // --- rendering --------------------------------------------------------------

  // describe turns the JSON of a custom value into words for a dialog or tooltip.
  function describe(detail) {
    try {
      const v = JSON.parse(detail);
      if (Array.isArray(v)) return `${v.length} rules`;
      return Object.entries(v).map(([k, x]) => `${k}: ${Array.isArray(x) ? x.join(", ") || "none" : x}`).join("; ");
    } catch {
      return detail;
    }
  }

  function labelOf(key) {
    return FEATURES.find((f) => f[0] === key)?.[1] || key;
  }

  function render() {
    if (state.error) {
      mount(body, h("div", { class: "issues bad" },
        h("div", { class: "head" }, "repos.yaml cannot be read"),
        h("ul", {}, state.error.split("\n").map((l) => h("li", {}, l))),
        h("p", { style: { padding: "0 12px 10px" } },
          h("a", { href: "#config/repos.yaml" }, "Open repos.yaml"), " to fix it.")));
      count.textContent = "";
      return;
    }
    if (!state.cfg) return;
    const rows = state.cfg.repos.filter((r) => !state.filter || r.name.toLowerCase().includes(state.filter));
    count.textContent = `${rows.length} of ${state.cfg.repos.length} repositories`;
    if (!rows.length) {
      mount(body, h("div", { class: "empty" }, state.cfg.repos.length ? "Nothing matches." : "No repository yet. Add one above."));
      return;
    }
    const head = h("tr", {},
      h("th", {}, "Repository"),
      FEATURES.map(([, label, title]) => h("th", { class: "cell-check", title }, label)),
      h("th", {}, "Agent"),
      h("th", { class: "cell-check", title: "pull requests the watcher already saw" }, "Seen"),
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
        row.use?.length ? h("span", { class: "chips", style: { display: "inline-flex", marginLeft: "6px" } },
          row.use.map((u) => h("span", { class: "chip", title: "profile in repos.yaml" }, u))) : null),
      FEATURES.map(([key]) => featureCell(row, key)),
      h("td", { "data-cell": `${row.name}|provider` }, providerSelect(row)),
      h("td", { class: "cell-check num muted" }, rule?.seen.length ? String(rule.seen.length) : "—"),
      h("td", { class: "actions" },
        h("button", {
          class: "btn ghost sm",
          "aria-expanded": String(open),
          title: open ? "Hide the rules and details" : "Show the rules and details",
          onclick: () => { open ? state.open.delete(row.name) : state.open.add(row.name); render(); },
        }, open ? "Hide" : "Details")),
    );
    return open ? [tr, detailRow(row, rule)] : [tr];
  }

  function featureCell(row, key) {
    const f = row.features[key];
    const box = h("input", {
      type: "checkbox",
      checked: f.on,
      "aria-label": `${labelOf(key)} on ${row.name}`,
      title: f.source === "profile" ? `On through the profile ${row.use?.join(", ")}` : "",
    });
    const td = h("td", { class: "cell-check", "data-cell": `${row.name}|${key}` }, box);
    box.addEventListener("change", () => toggle(row, key, box.checked, td));
    if (f.on && f.custom) {
      td.append(h("span", { class: "tuned", title: `Custom rules: ${describe(f.detail)}. Edit them in repos.yaml.` }, "⚙"));
    }
    return td;
  }

  function providerSelect(row) {
    const sel = h("select", { "aria-label": `Agent CLI for ${row.name}`, class: "provider" },
      h("option", { value: "" }, row.provider_source === "default" ? `default (${row.provider})` : "default (opencode)"),
      state.cfg.providers.map((p) => h("option", { value: p }, p)));
    sel.value = row.provider_source === "repo" ? row.provider : "";
    sel.addEventListener("change", () => save({ repo: row.name, key: "provider", value: sel.value || null }, { cell: sel.closest("td") }));
    return sel;
  }

  function detailRow(row, rule) {
    const own = rule?.entries || [];
    return h("tr", { class: "detail" },
      h("td", { colspan: String(FEATURES.length + 4) },
        h("div", { class: "stack", style: { gap: "10px" } },
          h("div", { class: "row" },
            h("a", { href: `https://github.com/${ghPath(row.name)}`, target: "_blank", rel: "noreferrer" }, "Open on GitHub"),
            row.checkout ? h("span", { class: "muted mono" }, `checkout ${row.checkout}`) : h("span", { class: "muted" }, "no local checkout"),
            h("span", { class: "spacer" }),
            h("button", { class: "btn danger sm", onclick: () => removeRepo(row) }, "Remove repository"),
          ),
          rule?.tower_ignore ? h("div", { class: "warning" },
            rule.tower_ignore === "missing"
              ? "Tower indexes this repository and .towerignore is missing. Run tower init, then list what it must skip."
              : "Tower indexes this repository and .towerignore holds no pattern. Add the paths and files Tower must skip.") : null,
          own.length ? h("div", { class: "stack", style: { gap: "6px" } },
            h("h3", { class: "muted" }, "Watcher rules"),
            own.map((e) => h("div", { class: "row", style: { fontSize: "12.5px" } },
              h("b", {}, e.workflow),
              h("span", { class: "muted" }, `on ${(e.on || []).join(", ")}`),
              e.ignore_authors?.length ? h("span", { class: "muted" }, `· skips ${e.ignore_authors.length} authors`) : null,
              e.only_authors?.length ? h("span", { class: "muted" }, `· only ${e.only_authors.join(", ")}`) : null,
            ))) : h("div", { class: "muted" }, "No watcher rule: this repository only takes part in tend or sync."),
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
