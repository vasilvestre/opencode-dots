import { $, h } from "./dom.js";
import { awf } from "./api.js";
import { startRouter } from "./router.js";
import { toast } from "./toast.js";
import { openModal } from "./modal.js";
import { mountActivity } from "./views/activity.js";
import { mountWorkflows } from "./views/workflows.js";
import { mountConfig } from "./views/config.js";
import { mountRepos } from "./views/repos.js";
import { mountCosts } from "./views/costs.js";
import { mountMemory } from "./views/memory.js";
import { startNotifications } from "./notifications.js";

const VIEWS = {
  activity: { mount: (el) => mountActivity(el), title: "Activity" },
  workflows: { mount: (el, arg) => mountWorkflows(el, arg), title: "Workflows" },
  config: { mount: (el, arg) => mountConfig(el, arg), title: "Config" },
  repos: { mount: (el) => mountRepos(el), title: "Repos" },
  costs: { mount: (el) => mountCosts(el), title: "Cost" },
  memory: { mount: (el) => mountMemory(el), title: "Memory" },
};

const viewEl = $("#view");
let current = null;

function show(route) {
  current?.destroy?.();
  current = null;
  const def = VIEWS[route.view] || VIEWS.activity;
  document.querySelectorAll("#tabs a").forEach((a) => {
    if (a.dataset.view === route.view) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
  document.title = `${def.title} · dots`;
  try {
    current = def.mount(viewEl, decodeURIComponent(route.arg));
  } catch (err) {
    console.error(err);
    viewEl.replaceChildren(h("div", { class: "empty" }, `This view failed to load: ${err.message}`));
  }
  viewEl.scrollTop = 0;
  window.scrollTo({ top: 0, behavior: "instant" });
}

startRouter(show);

const themeSelect = $("#theme");
const systemDark = matchMedia("(prefers-color-scheme: dark)");
themeSelect.value = document.documentElement.dataset.themeMode || "auto";
themeSelect.addEventListener("change", () => {
  const mode = themeSelect.value;
  localStorage.setItem("dots-theme", mode);
  document.documentElement.dataset.themeMode = mode;
  document.documentElement.dataset.theme =
    mode === "auto" ? (systemDark.matches ? "dark" : "light") : mode;
});
systemDark.addEventListener("change", () => {
  if (document.documentElement.dataset.themeMode === "auto") {
    document.documentElement.dataset.theme = systemDark.matches ? "dark" : "light";
  }
});

const pill = $("#awf-status");
const banner = $("#banner");
let wasDown = false;

async function ping() {
  try {
    await awf("/api/history/stats", { timeout: 6000 });
    pill.className = "pill on";
    pill.lastElementChild.textContent = "awf live";
    banner.hidden = true;
    if (wasDown) toast("awf is answering again", { tone: "ok" });
    wasDown = false;
  } catch {
    pill.className = "pill off";
    pill.lastElementChild.textContent = "awf down";
    banner.hidden = false;
    banner.textContent = "awf is not reachable on its API. Runs, history and workflow validation stay unavailable until you start it: make up";
    wasDown = true;
  }
}
ping();
setInterval(ping, 10000);
startNotifications();

// --- shortcuts -----------------------------------------------------------
document.addEventListener("keydown", (ev) => {
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName || "");
  if (ev.key === "?" && !typing) {
    ev.preventDefault();
    openModal({
      title: "Keyboard shortcuts",
      body: h("dl", { class: "kbd-help" },
        h("dt", {}, h("kbd", {}, "1"), "–", h("kbd", {}, "5")),
        h("dd", {}, "switch section"),
        h("dt", {}, h("kbd", {}, "Ctrl"), "+", h("kbd", {}, "S")),
        h("dd", {}, "save the open file, in the editor"),
        h("dt", {}, h("kbd", {}, "Esc")),
        h("dd", {}, "close a dialog"),
        h("dt", {}, h("kbd", {}, "?")),
        h("dd", {}, "this list"),
      ),
    });
  }
  if (!typing && /^[1-6]$/.test(ev.key) && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
    const view = ["activity", "workflows", "config", "repos", "costs", "memory"][Number(ev.key) - 1];
    location.hash = `#${view}`;
  }
});
