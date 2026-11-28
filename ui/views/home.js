import { h, mount } from "../dom.js";
import { getJSON, postJSON } from "../api.js";
import { toast } from "../toast.js";
import { ago, when } from "../format.js";
import { confirmDialog } from "../modal.js";
import { failed, skeleton } from "../state.js";
import { t, ts } from "../i18n.js";

// The repositories this viewer may see, with what the dot recorded for each. The server
// filters the list: a repository the viewer cannot see is in no row and no count.
export async function mountHome(el) {
  const head = h("p", { class: "muted" }, t("common.loading"));
  const table = h("div", { class: "table-wrap" }, skeleton());
  mount(el, h("div", { class: "stack" },
    h("div", { class: "section-head" }, h("h1", {}, t("common.home")), head),
    table,
  ));

  const root = el.firstChild;
  // The view may be left while a request is in flight: then it is no longer ours to fill.
  const gone = () => el.firstChild !== root;
  let rows;
  try {
    rows = await getJSON("/api/home");
  } catch (err) {
    head.textContent = "";
    mount(table, failed(t("home.cannot_read_the_repositories", [err.message]), () => mountHome(el)));
    return { destroy() {} };
  }

  // The emergency stop is the owner's: a visitor gets a 403 here and sees no control.
  let pause = null;
  try {
    pause = await getJSON("/api/pause");
  } catch {
    pause = null;
  }
  if (gone()) return { destroy() {} };
  const flip = async (action, repo) => {
    try {
      await postJSON("/api/pause", { action, repo: repo || "", reason: action === "stop" ? t("home.stopped_from_the_dashboard") : "" });
      toast(action === "stop" ? t("home.stopped", [repo || t("home.everything")]) : t("home.resumed", [repo || t("home.everything")]), { tone: "ok" });
      mountHome(el);
    } catch (err) {
      toast(t("home.cannot", [action, err.message]), { tone: "err" });
    }
  };
  const banner = pause && h("div", { class: pause.all ? "warning" : "toolbar" },
    pause.all
      ? [h("b", {}, t("home.the_dot_is_stopped")), h("span", {}, t("home.nothing_is_published_and_no", [pause.all.reason || "no reason given", when(pause.all.at)])),
        h("button", { class: "btn sm", onclick: () => flip("resume", "") }, t("home.resume_everything"))]
      : [h("button", { class: "btn danger sm", title: t("home.nothing_is_published_and_no_2"), onclick: async () => { if (await confirmDialog({ title: t("home.stop_everything"), message: t("home.no_run_starts_and_nothing"), confirm: t("home.stop_everything_2"), danger: true })) flip("stop", ""); } }, t("home.stop_everything_2"))]);
  if (banner) el.firstChild.insertBefore(banner, table);

  // The projects this machine works on (owner only; a visitor gets a 403 and sees nothing).
  let projects = null;
  try {
    // Not cloned first: those are the ones to act on.
    projects = (await getJSON("/api/checkouts")).projects.sort((a, b) => (a.state === "found") - (b.state === "found") || a.repo.localeCompare(b.repo));
  } catch {
    projects = null;
  }
  if (gone()) return { destroy() {} };
  const clone = async (repo) => {
    try {
      const r = await postJSON("/api/checkouts/clone", { repo });
      toast(t("home.cloning_into", [repo, r.target]), { tone: "ok" });
      setTimeout(() => mountHome(el), 1500);
    } catch (err) {
      toast(t("home.cannot_clone", [err.body?.error || err.message]), { tone: "err", timeout: 9000 });
    }
  };
  const projectsCard = projects && projects.length && h("div", { class: "stack" },
    h("h2", {}, t("home.projects_on_this_machine")),
    h("p", { class: "muted" }, t("home.the_repositories_this_machine_works")),
    h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, t("common.repository")), h("th", {}, t("home.state")), h("th", {}, t("home.where")), h("th", {}, ""))),
      h("tbody", {}, projects.map((p) => h("tr", {},
        h("td", {}, h("b", {}, p.repo)),
        h("td", {}, h("span", { class: `badge ${p.state === "found" ? "ok" : p.state === "failed" ? "failed" : "running"}` }, p.state === "found" ? ts("cloned") : p.state === "missing" ? t("home.not_cloned") : p.state)),
        h("td", {}, p.path || p.suggested
          ? h("code", {}, p.path || p.suggested)
          : h("span", { class: "muted" }, t("home.no_folder_suggested_add_one")),
        p.state === "missing" && p.suggested ? h("span", { class: "muted" }, t("home.suggested")) : null,
        p.error ? h("div", { class: "muted", title: p.error }, p.error) : null),
        h("td", {}, p.state === "missing" && p.suggested
          ? h("button", { class: "btn sm", onclick: () => clone(p.repo) }, t("home.clone"))
          : p.state === "failed" && p.suggested
            ? h("button", { class: "btn ghost sm", onclick: () => clone(p.repo) }, t("common.retry"))
            : null),
      ))))));
  // Above the table when something is not cloned (that is what needs attention), below it otherwise.
  if (projectsCard) el.firstChild.insertBefore(projectsCard, projects.some((p) => p.state !== "found") ? table : null);

  // What needs a person first, then the numbers. Each tile opens the page that acts on it.
  const [aut, links, health] = await Promise.all([
    getJSON("/api/autonomy").catch(() => null),
    getJSON("/api/links").catch(() => null),
    getJSON("/api/health").catch(() => null),
  ]);
  if (gone()) return { destroy() {} };
  const held = aut ? (aut.held || []).length : null;
  const open = links ? links.filter((p) => p.state === "open").length : null;
  const tile = (href, value, label, bad) => h("a", { class: `kpi${bad ? " bad" : ""}`, href, style: { textDecoration: "none", color: "inherit" } }, h("b", {}, String(value)), h("span", {}, label));
  const sum = (k) => rows.reduce((n, r) => n + (r[k] || 0), 0);
  el.firstChild.insertBefore(h("div", { class: "kpis", "aria-label": t("home.at_a_glance") },
    held == null ? null : tile("#autonomy", held, t("home.held_waiting_for_you"), held > 0),
    open == null ? null : tile("#links", open, t("home.link_proposals_to_decide"), open > 0),
    health?.present ? tile("#health", ts(health.overall), t("home.health"), health.overall !== "healthy") : null,
    tile("#activity", sum("runs7d"), t("home.runs_days")),
    tile("#costs", `$${sum("cost7d").toFixed(2)}`, t("home.cost_days")),
  ), el.firstChild.children[1]);

  head.textContent = t("home.repositor_runs_and_cost_cover", [rows.length, rows.length === 1 ? "y" : "ies"]);
  if (!rows.length) {
    mount(table, h("div", { class: "empty" }, t("home.no_repository_to_show_yet")));
    return { destroy() {} };
  }

  mount(table, h("table", {},
    h("thead", {}, h("tr", {},
      h("th", {}, t("common.repository")),
      h("th", {}, t("home.latest_activity")),
      h("th", { class: "num" }, t("common.runs")),
      h("th", { class: "num" }, t("common.cost")),
      h("th", { class: "num" }, t("common.held")),
      h("th", { class: "num" }, t("common.notes")),
      h("th", {}, t("common.memory")),
      pause ? h("th", {}, t("home.stop")) : null,
    )),
    h("tbody", {}, rows.map((r) => h("tr", {},
      h("td", {}, h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo))),
      h("td", {}, r.lastAt ? h("span", { title: when(r.lastAt) }, ago(r.lastAt)) : h("span", { class: "muted" }, t("home.none_yet"))),
      h("td", { class: "num" }, String(r.runs7d)),
      h("td", { class: "num" }, r.cost7d ? `$${r.cost7d.toFixed(4)}` : h("span", { class: "muted" }, "—")),
      h("td", { class: "num" }, r.held ? h("a", { href: "#autonomy" }, String(r.held)) : "0"),
      h("td", { class: "num" }, r.notes ? h("a", { href: "#autonomy" }, String(r.notes)) : "0"),
      h("td", {}, r.hasMemory ? h("a", { href: `#memory/${r.repo}` }, t("home.open")) : h("span", { class: "muted" }, "—")),
      pause ? h("td", {}, pause.repos?.[r.repo]
        ? [h("span", { class: "badge failed", title: pause.repos[r.repo].reason || "" }, t("home.stopped_2")), h("button", { class: "btn ghost sm", onclick: () => flip("resume", r.repo) }, t("home.resume"))]
        : h("button", { class: "btn ghost sm", disabled: !!pause.all, onclick: () => flip("stop", r.repo) }, t("home.stop"))) : null,
    ))),
  ));
  return { destroy() {} };
}
