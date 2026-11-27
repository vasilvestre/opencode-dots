import { h, mount } from "../dom.js";
import { getJSON, postJSON } from "../api.js";
import { toast } from "../toast.js";
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

  // The emergency stop is the owner's: a visitor gets a 403 here and sees no control.
  let pause = null;
  try {
    pause = await getJSON("/api/pause");
  } catch {
    pause = null;
  }
  const flip = async (action, repo) => {
    try {
      await postJSON("/api/pause", { action, repo: repo || "", reason: action === "stop" ? "stopped from the dashboard" : "" });
      toast(action === "stop" ? `Stopped ${repo || "everything"}` : `Resumed ${repo || "everything"}`, { tone: "ok" });
      mountHome(el);
    } catch (err) {
      toast(`Cannot ${action}: ${err.message}`, { tone: "err" });
    }
  };
  const banner = pause && h("div", { class: pause.all ? "warning" : "toolbar" },
    pause.all
      ? [h("b", {}, "The dot is stopped. "), h("span", {}, `Nothing is published and no run starts (${pause.all.reason || "no reason given"}, since ${when(pause.all.at)}). `),
        h("button", { class: "btn sm", onclick: () => flip("resume", "") }, "Resume everything")]
      : [h("button", { class: "btn danger sm", title: "Nothing is published and no run starts until you resume", onclick: () => { if (confirm("Stop everything? No run starts and nothing is published until you resume.")) flip("stop", ""); } }, "Stop everything")]);
  if (banner) el.firstChild.insertBefore(banner, table);

  // The projects this machine works on (owner only; a visitor gets a 403 and sees nothing).
  let projects = null;
  try {
    projects = (await getJSON("/api/checkouts")).projects;
  } catch {
    projects = null;
  }
  const clone = async (repo) => {
    try {
      const r = await postJSON("/api/checkouts/clone", { repo });
      toast(`Cloning ${repo} into ${r.target}`, { tone: "ok" });
      setTimeout(() => mountHome(el), 1500);
    } catch (err) {
      toast(`Cannot clone: ${err.body?.error || err.message}`, { tone: "err", timeout: 9000 });
    }
  };
  const projectsCard = projects && projects.length && h("div", { class: "stack" },
    h("h2", {}, "Projects on this machine"),
    h("p", { class: "muted" }, "The repositories this machine works on, and where each lives. A project that is not cloned runs on a private copy; clone it where your team keeps its projects, or say where yours are in ~/.config/dots/paths."),
    h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", {}, "State"), h("th", {}, "Where"), h("th", {}, ""))),
      h("tbody", {}, projects.map((p) => h("tr", {},
        h("td", {}, h("b", {}, p.repo)),
        h("td", {}, h("span", { class: `badge ${p.state === "found" ? "ok" : p.state === "failed" ? "failed" : "running"}` }, p.state === "found" ? "cloned" : p.state === "missing" ? "not cloned" : p.state)),
        h("td", {}, p.path || p.suggested
          ? h("code", {}, p.path || p.suggested)
          : h("span", { class: "muted" }, "no folder suggested: add one to ~/.config/dots/paths"),
        p.state === "missing" && p.suggested ? h("span", { class: "muted" }, " (suggested)") : null,
        p.error ? h("div", { class: "muted", title: p.error }, p.error) : null),
        h("td", {}, p.state === "missing" && p.suggested
          ? h("button", { class: "btn sm", onclick: () => clone(p.repo) }, "Clone")
          : p.state === "failed" && p.suggested
            ? h("button", { class: "btn ghost sm", onclick: () => clone(p.repo) }, "Retry")
            : null),
      ))))));
  if (projectsCard) el.firstChild.appendChild(projectsCard);

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
      pause ? h("th", {}, "Stop") : null,
    )),
    h("tbody", {}, rows.map((r) => h("tr", {},
      h("td", {}, h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo))),
      h("td", {}, r.lastAt ? h("span", { title: when(r.lastAt) }, ago(r.lastAt)) : h("span", { class: "muted" }, "none yet")),
      h("td", { class: "num" }, String(r.runs7d)),
      h("td", { class: "num" }, r.cost7d ? `$${r.cost7d.toFixed(4)}` : h("span", { class: "muted" }, "—")),
      h("td", { class: "num" }, String(r.held)),
      h("td", { class: "num" }, String(r.notes)),
      h("td", {}, r.hasMemory ? h("a", { href: `#memory/${r.repo}` }, "Open") : h("span", { class: "muted" }, "—")),
      pause ? h("td", {}, pause.repos?.[r.repo]
        ? [h("span", { class: "badge failed", title: pause.repos[r.repo].reason || "" }, "stopped "), h("button", { class: "btn ghost sm", onclick: () => flip("resume", r.repo) }, "Resume")]
        : h("button", { class: "btn ghost sm", disabled: !!pause.all, onclick: () => flip("stop", r.repo) }, "Stop")) : null,
    ))),
  ));
  return { destroy() {} };
}
