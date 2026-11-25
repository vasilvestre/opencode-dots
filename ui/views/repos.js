import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";

// awf-gh groups its entries per repository on the server side, so one card
// shows every workflow that repository triggers and the PRs already seen.
export function mountRepos(el) {
  const state = { repos: [], filter: "" };
  const search = h("input", {
    type: "search",
    placeholder: "Filter repositories",
    "aria-label": "Filter repositories",
    oninput: () => { state.filter = search.value.trim().toLowerCase(); render(); },
  });
  const count = h("span", { class: "muted" });
  const list = h("div", { class: "list" });

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {}, h("h1", {}, "Repositories"), h("p", { class: "muted" },
        "What the watcher does on each repository, generated from ",
        h("a", { href: "#config/repos.yaml" }, "repos.yaml"),
        ". The list is private and stays out of git.")),
      h("div", { class: "row" }, search),
    ),
    h("div", { class: "section-head" }, count),
    list,
  ));

  function render() {
    const rows = state.repos.filter((r) => !state.filter || r.repo.toLowerCase().includes(state.filter));
    count.textContent = `${rows.length} of ${state.repos.length} repositories`;
    if (!rows.length) {
      mount(list, h("div", { class: "empty" }, state.repos.length ? "Nothing matches." : "No repository in repos.yaml."));
      return;
    }
    mount(list, rows.map((r) => h("div", { class: "item" },
      h("div", { class: "row between" },
        h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo)),
        r.seen.length ? h("span", { class: "muted", style: { fontSize: "12px" } }, `${r.seen.length} seen`) : null,
      ),
      // Tower indexes this checkout, so a missing or empty ignore file turns
      // the index into noise on the next run.
      r.tower_ignore ? h("div", { class: "warning", style: { marginTop: "10px" } },
        r.tower_ignore === "missing"
          ? "Tower indexes this repository and .towerignore is missing. Run tower init, then list what it must skip."
          : "Tower indexes this repository and .towerignore holds no pattern. Add the paths and files Tower must skip.",
      ) : null,
      h("div", { class: "stack", style: { gap: "6px", marginTop: "10px" } },
        r.entries.map((e) => h("div", { class: "row", style: { fontSize: "12.5px" } },
          h("span", { class: "badge" }, e.scope),
          h("b", {}, e.workflow),
          h("span", { class: "muted" }, `on ${(e.on || []).join(", ")}`),
          e.ignore_authors?.length ? h("span", { class: "muted" }, `· skips ${e.ignore_authors.length} authors`) : null,
          e.only_authors?.length ? h("span", { class: "muted" }, `· only ${e.only_authors.join(", ")}`) : null,
        )),
      ),
      r.seen.length ? h("div", { class: "chips", style: { marginTop: "10px" } },
        r.seen.slice().reverse().map((pr) => h("a", {
          class: "chip",
          href: `https://github.com/${r.repo}/pull/${pr}`,
          target: "_blank",
          rel: "noreferrer",
        }, `#${pr}`))) : null,
    )));
  }

  getJSON("/api/repos")
    .then((repos) => { state.repos = repos; render(); })
    .catch((err) => mount(list, h("div", { class: "empty" }, `Cannot list repositories (${err.message}).`)));

  return { destroy() {} };
}
