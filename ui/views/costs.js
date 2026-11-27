import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";

// What each pull request has cost so far, with the workflows that spent it.
// The grouping comes from the run logs, so the totals only cover the runs the
// dashboard recorded. A run started before awf reported `cost_usd` has no
// price, and the page says how many of those it skipped.
export async function mountCosts(el) {
  const head = h("p", { class: "muted" }, "Loading…");
  const table = h("div", { class: "table-wrap" });
  mount(el, h("div", { class: "stack" },
    h("div", { class: "section-head" }, h("h2", {}, "Cost"), head),
    table,
  ));

  let data;
  try {
    data = await getJSON("/api/usage");
  } catch (err) {
    head.textContent = `Cannot read the usage: ${err.message}`;
    return { destroy() {} };
  }

  const priced = data.runs - data.unpriced;
  head.textContent = `$${data.total.toFixed(4)} over ${priced} priced run${priced === 1 ? "" : "s"}`
    + (data.unpriced ? ` · ${data.unpriced} run${data.unpriced === 1 ? "" : "s"} recorded before awf reported a price` : "");

  if (!data.groups.length) {
    mount(table, h("div", { class: "empty" }, "No recorded run yet. Costs appear once the dashboard has watched a run finish."));
    return { destroy() {} };
  }

  mount(table, h("table", {},
    h("thead", {}, h("tr", {},
      h("th", {}, "Pull request"),
      h("th", { class: "num" }, "Runs"),
      h("th", { class: "num" }, "Total"),
      h("th", {}, "Workflows"),
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

// rowLabel links the pull request to GitHub when the repository is known.
function rowLabel(g) {
  if (!g.repo) return h("b", {}, g.key === "(no pull request)" ? "Unattributed runs" : g.key);
  if (!g.pr) return h("a", { href: `https://github.com/${g.repo}`, target: "_blank", rel: "noreferrer" }, `${g.repo} (no pull request)`);
  const kind = g.issue && g.issue === g.pr ? "issues" : "pull";
  return h("a", { href: `https://github.com/${g.repo}/${kind}/${g.pr}`, target: "_blank", rel: "noreferrer" }, g.key);
}
