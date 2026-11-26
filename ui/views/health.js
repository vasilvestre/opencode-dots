import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { ago, when } from "../format.js";

const STALE_MS = 30 * 60 * 1000;
const OVERALL = { healthy: "ok", degraded: "warn", down: "failed" };
const CHECK = { ok: "ok", warn: "warn", fail: "failed" };

// What bin/health --json last wrote to STATE_DIR/health.json. Read only.
export function mountHealth(el) {
  const out = h("div", { class: "stack" });
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, "Health"),
      h("p", { class: "muted" }, "The last answer of bin/health. This page runs no check.")),
    out,
  ));

  getJSON("/api/health")
    .then((data) => {
      if (!data.present) {
        mount(out, h("div", { class: "empty" }, "No health data yet."));
        return;
      }
      const stale = Date.now() - new Date(data.mtime).getTime() > STALE_MS;
      if (stale) {
        mount(out, h("div", { class: "empty" },
          `No health data yet: the last file is stale (${ago(data.mtime)}, over 30 minutes). Is dots-health.timer running?`));
        return;
      }
      mount(out,
        h("div", {},
          h("span", { class: `badge ${OVERALL[data.overall] || "warn"}` }, data.overall),
          " ",
          h("span", { class: "muted", title: when(data.mtime) }, `checked ${ago(data.mtime)}`)),
        h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Check", "Status", "Detail"].map((c) => h("th", {}, c)))),
          h("tbody", {}, (data.checks || []).map((c) => h("tr", {},
            h("td", {}, c.name || "?"),
            h("td", {}, h("span", { class: `badge ${CHECK[c.status] || ""}` }, c.status || "?")),
            h("td", {}, c.detail || ""),
          ))),
        )));
    })
    .catch((err) => mount(out, h("div", { class: "empty" }, `Cannot read the health data (${err.message}).`)));

  return { destroy() {} };
}
