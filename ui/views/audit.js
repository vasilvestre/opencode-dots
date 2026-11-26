import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { ago, when } from "../format.js";

const LIMITS = [50, 100, 500];

// The last writes the dashboard served (ADR 0037), newest first. Read only: a write is
// recorded with its endpoint, target, caller and status, never its body.
export function mountAudit(el) {
  const out = h("div", {});
  let limit = 100;
  const pick = h("select", {
    "aria-label": "How many entries",
    onchange: () => { limit = Number(pick.value); load(); },
  }, LIMITS.map((n) => h("option", { value: String(n), selected: n === limit }, `Last ${n}`)));

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, "Audit"),
        h("p", { class: "muted" }, "Every write the dashboard served: who, where, and how it ended. Bodies are never kept.")),
      h("div", { class: "row" }, pick)),
    out,
  ));
  load();

  function load() {
    getJSON(`/api/audit?limit=${limit}`)
      .then((rows) => {
        if (!rows.length) {
          mount(out, h("div", { class: "empty" }, "No write recorded yet."));
          return;
        }
        mount(out, h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, ["When", "Caller", "Endpoint", "Target", "Status"].map((c) => h("th", {}, c)))),
          h("tbody", {}, rows.map((r) => h("tr", {},
            h("td", { title: when(r.at) }, ago(r.at)),
            h("td", {}, r.caller),
            h("td", { class: "mono" }, r.endpoint),
            h("td", { class: "mono" }, r.target || ""),
            h("td", {}, h("span", { class: `badge ${r.outcome < 400 ? "ok" : "failed"}` }, String(r.outcome))),
          ))),
        )));
      })
      .catch((err) => mount(out, h("div", { class: "empty" }, `Cannot read the audit log (${err.message}).`)));
  }

  return { destroy() {} };
}
