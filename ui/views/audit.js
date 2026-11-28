import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { failed, skeleton } from "../state.js";
import { ago, when } from "../format.js";
import { t } from "../i18n.js";

const LIMITS = [50, 100, 500];

// The last writes the dashboard served (ADR 0037), newest first. Read only: a write is
// recorded with its endpoint, target, caller and status, never its body.
export function mountAudit(el) {
  const out = h("div", {});
  let limit = 100;
  let rowsAll = [];
  const find = h("input", { type: "search", placeholder: t("audit.filter_caller_endpoint_target"), "aria-label": t("audit.filter_the_audit_log"), oninput: () => paint() });
  const pick = h("select", {
    "aria-label": t("audit.how_many_entries"),
    onchange: () => { limit = Number(pick.value); load(); },
  }, LIMITS.map((n) => h("option", { value: String(n), selected: n === limit }, t("audit.last", [n]))));

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {},
        h("h1", {}, t("common.audit")),
        h("p", { class: "muted" }, t("audit.every_write_the_dashboard_served"))),
      h("div", { class: "row" }, find, pick)),
    out,
  ));
  load();

  function load() {
    mount(out, skeleton());
    getJSON(`/api/audit?limit=${limit}`)
      .then((r) => { rowsAll = r; paint(); })
      .catch((err) => mount(out, failed(t("audit.cannot_read_the_audit_log", [err.message]), load)));
  }

  function paint() {
    const q = find.value.trim().toLowerCase();
    const rows = rowsAll.filter((r) => !q || `${r.caller} ${r.endpoint} ${r.target || ""} ${r.outcome}`.toLowerCase().includes(q));
    {
      {
        if (!rows.length) {
          mount(out, h("div", { class: "empty" }, t("audit.no_write_recorded_yet")));
          return;
        }
        mount(out, h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, [t("audit.when"), t("audit.caller"), t("audit.endpoint"), t("common.target"), t("common.status")].map((c) => h("th", {}, c)))),
          h("tbody", {}, rows.map((r) => h("tr", {},
            h("td", { title: when(r.at) }, ago(r.at)),
            h("td", {}, r.caller),
            h("td", { class: "mono" }, r.endpoint),
            h("td", { class: "mono" }, r.target || ""),
            h("td", {}, h("span", { class: `badge ${r.outcome < 400 ? "ok" : "failed"}` }, String(r.outcome))),
          ))),
        )));
      }
    }
  }

  return { destroy() {} };
}
