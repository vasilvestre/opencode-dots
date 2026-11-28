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
  const workersEl = h("div", {});
  const claimsEl = h("div", {});
  const queueEl = h("div", {});
  mount(el, h("div", { class: "stack" },
    h("div", {}, h("h1", {}, t("common.health")),
      h("p", { class: "muted" }, t("health.the_last_answer_of_bin"))),
    out,
    queueEl,
    workersEl,
    claimsEl,
  ));
  loadWorkers();
  loadQueueWorkers();
  loadClaims();

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
      const rank = { fail: 0, warn: 1, ok: 2 };
      const checks = (data.checks || []).slice().sort((a, b) => (rank[a.status] ?? 1) - (rank[b.status] ?? 1));
      const n = (st) => checks.filter((c) => c.status === st).length;
      mount(out,
        h("div", { class: "kpis" },
          h("div", { class: "kpi" }, h("b", {}, String(n("ok"))), h("span", {}, ts("ok"))),
          h("div", { class: `kpi${n("warn") ? " bad" : ""}` }, h("b", {}, String(n("warn"))), h("span", {}, ts("warn"))),
          h("div", { class: `kpi${n("fail") ? " bad" : ""}` }, h("b", {}, String(n("fail"))), h("span", {}, ts("fail")))),
        h("div", {},
          h("span", { class: `badge ${OVERALL[data.overall] || "warn"}` }, ts(data.overall)),
          " ",
          h("span", { class: "muted", title: when(data.mtime) }, t("health.checked", [ago(data.mtime)]))),
        h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, [t("health.check"), t("common.status"), t("health.detail")].map((c) => h("th", {}, c)))),
          h("tbody", {}, checks.map((c) => h("tr", {},
            h("td", {}, c.name || "?"),
            h("td", {}, h("span", { class: `badge ${CHECK[c.status] || ""}` }, c.status ? ts(c.status) : "?")),
            h("td", {}, c.detail || ""),
          ))),
        )));
    })
    .catch((err) => mount(out, failed(t("health.cannot_read_the_health_data", [err.message]), () => mountHealth(el))));

  // The workers pulling jobs from the queue: who they are, which agent CLIs they run, and when they
  // last read it. A job that needs an agent no worker runs waits until it expires.
  async function loadQueueWorkers() {
    let list = [];
    try { list = (await getJSON("/api/queue/workers")).workers || []; } catch { return; }
    if (!list.length) return;
    mount(queueEl, h("div", { class: "card" },
      h("div", { class: "card-head" }, h("h3", {}, t("health.queue_workers")),
        h("span", { class: "badge ok" }, `${list.length}`)),
      h("p", { class: "muted" }, t("health.queue_workers_hint")),
      h("div", { class: "list" }, list.map((w) =>
        h("div", { class: "item row between" },
          h("div", {}, h("span", { class: "mono" }, w.name), " ",
            (w.agents || []).length
              ? (w.agents).map((a) => h("span", { class: "badge", style: { marginRight: "4px" } }, a))
              : h("span", { class: "muted" }, t("health.queue_agents_unknown"))),
          h("div", { class: "muted" }, t("health.queue_last_read", [ago(w.seen)])))))));
  }

  // A claim is a veto: the dot does not push to a branch a person holds (ADR 0026).
  // The list is short and changes slowly, so one read on mount is enough.
  async function loadClaims() {
    let list = [];
    try { list = await getJSON("/api/claims"); } catch { return; }
    if (!list.length) return;
    mount(claimsEl, h("div", { class: "card" },
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
    mount(workersEl, h("div", { class: "card" },
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

  return { destroy() {} };
}
