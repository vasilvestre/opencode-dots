import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { failed, skeleton } from "../state.js";
import { t } from "../i18n.js";

// What each pull request has cost so far, with the workflows that spent it.
// The grouping comes from the run logs, so the totals only cover the runs the
// dashboard recorded. A run started before awf reported `cost_usd` has no
// price, and the page says how many of those it skipped.
export async function mountCosts(el) {
  const head = h("p", { class: "muted" }, t("common.loading"));
  const table = h("div", { class: "table-wrap" }, skeleton());
  const bars = h("div", { class: "two-col" });
  mount(el, h("div", { class: "stack" },
    h("div", { class: "section-head" }, h("h2", {}, t("common.cost")), head),
    bars,
    table,
  ));

  let data;
  try {
    data = await getJSON("/api/usage");
  } catch (err) {
    head.textContent = "";
    mount(table, failed(t("costs.cannot_read_the_usage", [err.message]), () => mountCosts(el)));
    return { destroy() {} };
  }

  const priced = data.runs - data.unpriced;
  head.textContent = t("costs.over_priced_run", [data.total.toFixed(4), priced, priced === 1 ? "" : "s"])
    + (data.unpriced ? t("costs.run_recorded_before_awf_reported", [data.unpriced, data.unpriced === 1 ? "" : "s"]) : "");

  if (!data.groups.length) {
    mount(table, h("div", { class: "empty" }, t("costs.no_recorded_run_yet_costs")));
    return { destroy() {} };
  }

  // Where the money went, as bars (no chart library).
  const byWf = new Map();
  const byRepo = new Map();
  for (const g of data.groups) {
    const r = g.repo || "unattributed";
    byRepo.set(r, (byRepo.get(r) || 0) + (g.cost || 0));
    for (const w of g.workflows) byWf.set(w.workflow, (byWf.get(w.workflow) || 0) + (w.cost || 0));
  }
  mount(bars, barCard(t("costs.by_workflow"), byWf), barCard(t("costs.by_repository"), byRepo));

  mount(table, h("table", {},
    h("thead", {}, h("tr", {},
      h("th", {}, t("costs.pull_request")),
      h("th", { class: "num" }, t("common.runs")),
      h("th", { class: "num" }, t("costs.total")),
      h("th", {}, t("common.workflows")),
    )),
    h("tbody", {}, data.groups.map((g) => h("tr", {},
      h("td", {}, rowLabel(g)),
      h("td", { class: "num" }, String(g.runs)),
      h("td", { class: "num" }, g.cost ? `$${g.cost.toFixed(4)}` : h("span", { class: "muted" }, "—")),
      h("td", {}, h("div", { class: "chips" }, g.workflows.map((w) => h("span", { class: "chip" },
        h("b", {}, w.workflow),
        w.cost ? ` $${w.cost.toFixed(4)}` : "",
        w.runs > 1 ? h("span", { class: "muted" }, ` ×${w.runs}`) : "",
      )))),
    ))),
  ));
  return { destroy() {} };
}

function barCard(title, totals) {
  const top = [...totals].filter(([, v]) => v > 0).sort((a, b) => b[1] - a[1]).slice(0, 8);
  const max = top[0]?.[1] || 1;
  return h("div", { class: "card" }, h("h3", {}, title),
    top.length ? h("div", { class: "bars" }, top.map(([k, v]) => h("div", { class: "bar-row" },
      h("span", { class: "bar-label", title: k }, k),
      h("span", { class: "bar-track" }, h("i", { style: { width: `${Math.max(2, (v / max) * 100)}%` } })),
      h("span", { class: "num" }, `$${v.toFixed(4)}`))))
      : h("p", { class: "muted" }, t("costs.no_priced_run")));
}

// rowLabel links the pull request to GitHub when the repository is known.
function rowLabel(g) {
  if (!g.repo) return h("b", {}, g.key === "(no pull request)" ? t("costs.unattributed_runs") : g.key);
  if (!g.pr) return h("a", { href: `https://github.com/${g.repo}`, target: "_blank", rel: "noreferrer" }, t("costs.no_pull_request_2", [g.repo]));
  const kind = g.issue && g.issue === g.pr ? "issues" : "pull";
  return h("a", { href: `https://github.com/${g.repo}/${kind}/${g.pr}`, target: "_blank", rel: "noreferrer" }, g.key);
}
