import { $, h } from "./dom.js";
import { awf } from "./api.js";
import { startRouter, navigate } from "./router.js";
import { OWNER_ONLY, isVisitor, shapeNav } from "./session.js";
import { toast } from "./toast.js";
import { openModal } from "./modal.js";
import { mountHome } from "./views/home.js";
import { mountActivity } from "./views/activity.js";
import { mountWorkflows } from "./views/workflows.js";
import { mountConfig } from "./views/config.js";
import { mountRepos } from "./views/repos.js";
import { mountCosts } from "./views/costs.js";
import { mountMemory } from "./views/memory.js";
import { mountAutonomy } from "./views/autonomy.js";
import { mountHealth } from "./views/health.js";
import { mountAudit } from "./views/audit.js";
import { mountLinks } from "./views/links.js";
import { startNotifications } from "./notifications.js";
import { openPalette } from "./palette.js";
import { sortableTables } from "./table.js";
import { getJSON } from "./api.js";

const VIEWS = {
  home: { mount: (el) => mountHome(el), title: t("common.home") },
  activity: { mount: (el) => mountActivity(el), title: t("common.activity") },
  workflows: { mount: (el, arg) => mountWorkflows(el, arg), title: t("common.workflows") },
  config: { mount: (el, arg) => mountConfig(el, arg), title: t("common.settings") },
  repos: { mount: (el) => mountRepos(el), title: t("common.repos") },
  costs: { mount: (el) => mountCosts(el), title: t("common.cost") },
  memory: { mount: (el, arg) => mountMemory(el, arg), title: t("common.memory") },
  autonomy: { mount: (el) => mountAutonomy(el), title: t("common.autonomy") },
  health: { mount: (el) => mountHealth(el), title: t("common.health") },
  audit: { mount: (el) => mountAudit(el), title: t("common.audit") },
  links: { mount: (el) => mountLinks(el), title: t("common.links") },
};

const viewEl = $("#view");
let current = null;

function show(route) {
  // A visitor is not offered the owner's sections, and typing one in the address bar leads back.
  if (isVisitor() && OWNER_ONLY.has(route.view)) {
    navigate("home", "", { replace: true });
    return;
  }
  current?.destroy?.();
  current = null;
  const def = VIEWS[route.view] || VIEWS.home;
  document.querySelectorAll("#tabs a").forEach((a) => {
    if (a.dataset.view === route.view) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
  document.title = `${def.title} · dots`;
  try {
    current = def.mount(viewEl, decodeURIComponent(route.arg));
  } catch (err) {
    console.error(err);
    viewEl.replaceChildren(h("div", { class: "empty" }, t("boot.this_view_failed_to_load", [err.message])));
  }
  viewEl.scrollTop = 0;
  window.scrollTo({ top: 0, behavior: "instant" });
}

translatePage();
shapeNav();
startRouter(show);
sortableTables(viewEl);

// What waits for a decision, as a count on the tab: held items and open link proposals.
function badge(view, n) {
  const a = document.querySelector(`#tabs a[data-view="${view}"]`);
  if (!a) return;
  a.querySelector(".count")?.remove();
  if (n > 0) a.append(h("span", { class: "count" }, String(n), h("span", { class: "sr-only" }, t("boot.waiting"))));
}
async function counts() {
  const [aut, links] = await Promise.all([getJSON("/api/autonomy").catch(() => null), getJSON("/api/links").catch(() => null)]);
  if (aut) badge("autonomy", (aut.held || []).length);
  if (links) badge("links", links.filter((p) => p.state === "open").length);
}
counts();
setInterval(counts, 30000);

import "./theme.js";
import { t, translatePage } from "./i18n.js";

$("#palette-btn").addEventListener("click", () => openPalette());

const pill = $("#awf-status");
const banner = $("#banner");
let wasDown = false;

async function ping() {
  try {
    await awf("/api/history/stats", { timeout: 6000 });
    pill.className = "pill on";
    pill.lastElementChild.textContent = t("boot.awf_live");
    banner.hidden = true;
    if (wasDown) toast(t("boot.awf_is_answering_again"), { tone: "ok" });
    wasDown = false;
  } catch {
    pill.className = "pill off";
    pill.lastElementChild.textContent = t("boot.awf_down");
    banner.hidden = false;
    banner.textContent = t("boot.awf_is_not_reachable_on");
    wasDown = true;
  }
}
// awf and its history belong to the owner: a visitor's calls to them would only be refused.
if (!isVisitor()) {
  ping();
  setInterval(ping, 10000);
  startNotifications();
}

// --- shortcuts -----------------------------------------------------------
document.addEventListener("keydown", (ev) => {
  if ((ev.ctrlKey || ev.metaKey) && !ev.altKey && ev.key.toLowerCase() === "k") {
    ev.preventDefault();
    if (!document.querySelector(".modal-backdrop")) openPalette();
    return;
  }
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName || "");
  if (ev.key === "?" && !typing) {
    ev.preventDefault();
    openModal({
      title: t("boot.keyboard_shortcuts"),
      body: h("dl", { class: "kbd-help" },
        h("dt", {}, h("kbd", {}, "1"), "–", h("kbd", {}, "9"), ", ", h("kbd", {}, "0")),
        h("dd", {}, t("boot.switch_section")),
        h("dt", {}, h("kbd", {}, "Ctrl"), "+", h("kbd", {}, "K")),
        h("dd", {}, t("boot.go_to_a_section_workflow")),
        h("dt", {}, h("kbd", {}, "Ctrl"), "+", h("kbd", {}, "S")),
        h("dd", {}, t("boot.save_the_open_file_in")),
        h("dt", {}, h("kbd", {}, "Esc")),
        h("dd", {}, t("boot.close_a_dialog")),
        h("dt", {}, h("kbd", {}, "?")),
        h("dd", {}, t("boot.this_list")),
      ),
    });
  }
  if (!typing && /^[0-9]$/.test(ev.key) && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
    const view = ["home", "activity", "autonomy", "links", "workflows", "repos", "config", "costs", "health", "memory"][(Number(ev.key) + 9) % 10];
    location.hash = `#${view}`;
  }
});
