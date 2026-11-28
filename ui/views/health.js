import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { failed, skeleton } from "../state.js";
import { ago, when } from "../format.js";
import { t, ts } from "../i18n.js";

const STALE_MS = 30 * 60 * 1000;
const OVERALL = { healthy: "ok", degraded: "warn", down: "failed" };
const CHECK = { ok: "ok", warn: "warn", fail: "failed" };

// What bin/health --json last wrote to STATE_DIR/health.json. Read only.
export function mountHealth(el) {
  const out = h("div", { class: "stack" }, skeleton());
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, t("common.health")),
      h("p", { class: "muted" }, t("health.the_last_answer_of_bin"))),
    out,
  ));

  getJSON("/api/health")
    .then((data) => {
      if (!data.present) {
        mount(out, h("div", { class: "empty" }, t("health.no_health_data_yet")));
        return;
      }
      const stale = Date.now() - new Date(data.mtime).getTime() > STALE_MS;
      if (stale) {
        mount(out, h("div", { class: "empty" },
          t("health.the_last_health_check_is", [ago(data.mtime)])));
        return;
      }
      mount(out,
        h("div", {},
          h("span", { class: `badge ${OVERALL[data.overall] || "warn"}` }, ts(data.overall)),
          " ",
          h("span", { class: "muted", title: when(data.mtime) }, t("health.checked", [ago(data.mtime)]))),
        h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, [t("health.check"), t("common.status"), t("health.detail")].map((c) => h("th", {}, c)))),
          h("tbody", {}, (data.checks || []).map((c) => h("tr", {},
            h("td", {}, c.name || "?"),
            h("td", {}, h("span", { class: `badge ${CHECK[c.status] || ""}` }, c.status ? ts(c.status) : "?")),
            h("td", {}, c.detail || ""),
          ))),
        )));
    })
    .catch((err) => mount(out, failed(t("health.cannot_read_the_health_data", [err.message]), () => mountHealth(el))));

  return { destroy() {} };
}
