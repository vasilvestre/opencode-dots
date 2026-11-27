import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { ago, when } from "../format.js";

// The repositories this viewer may see, with what the dot recorded for each. The server
// filters the list: a repository the viewer cannot see is in no row and no count.
export async function mountHome(el) {
  const head = h("p", { class: "muted" }, "Loading…");
  const table = h("div", { class: "table-wrap" });
  mount(el, h("div", { class: "stack" },
    h("div", { class: "section-head" }, h("h1", {}, "Home"), head),
    table,
  ));

  let rows;
  try {
    rows = await getJSON("/api/home");
  } catch (err) {
    head.textContent = `Cannot read the repositories: ${err.message}`;
    return { destroy() {} };
  }

  head.textContent = `${rows.length} repositor${rows.length === 1 ? "y" : "ies"}; runs and cost cover the last 7 days`;
  if (!rows.length) {
    mount(table, h("div", { class: "empty" }, "No repository to show yet."));
    return { destroy() {} };
  }

  mount(table, h("table", {},
    h("thead", {}, h("tr", {},
      h("th", {}, "Repository"),
      h("th", {}, "Latest activity"),
      h("th", { class: "num" }, "Runs"),
      h("th", { class: "num" }, "Cost"),
      h("th", { class: "num" }, "Held"),
      h("th", { class: "num" }, "Notes"),
      h("th", {}, "Memory"),
    )),
    h("tbody", {}, rows.map((r) => h("tr", {},
      h("td", {}, h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo))),
      h("td", {}, r.lastAt ? h("span", { title: when(r.lastAt) }, ago(r.lastAt)) : h("span", { class: "muted" }, "none yet")),
      h("td", { class: "num" }, String(r.runs7d)),
      h("td", { class: "num" }, r.cost7d ? `$${r.cost7d.toFixed(4)}` : h("span", { class: "muted" }, "—")),
      h("td", { class: "num" }, String(r.held)),
      h("td", { class: "num" }, String(r.notes)),
      h("td", {}, r.hasMemory ? h("a", { href: `#memory/${r.repo}` }, "Open") : h("span", { class: "muted" }, "—")),
    ))),
  ));
  return { destroy() {} };
}
