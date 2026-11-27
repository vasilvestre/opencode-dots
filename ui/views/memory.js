import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";

// What the daily pass learned per repository: rules it derives from reviews,
// and facts it keeps between passes.
export function mountMemory(el, arg = "") {
  const search = h("input", {
    type: "search",
    placeholder: "Filter repositories",
    value: arg,
    "aria-label": "Filter memory by repository",
    oninput: () => { filter = search.value.trim().toLowerCase(); render(); },
  });
  const list = h("div", { class: "list" });
  let repos = [];
  let filter = arg.trim().toLowerCase();

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {}, h("h1", {}, "Memory"), h("p", { class: "muted" }, "Written by tend-repo. Rules change behaviour; facts inform it.")),
      search,
    ),
    list,
  ));

  function render() {
    const rows = repos.filter((r) => !filter || r.repo.toLowerCase().includes(filter));
    if (!rows.length) {
      mount(list, h("div", { class: "empty" }, repos.length ? "Nothing matches." : "No pass has written lessons or memory yet."));
      return;
    }
    mount(list, rows.map((r) => h("div", { class: "card" },
      h("div", { class: "row between" },
        h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo)),
        r.since ? h("span", { class: "muted", style: { fontSize: "12px" } }, `covered up to ${r.since}`) : null,
      ),
      r.wouldIssue || r.wouldPr ? h("div", { style: { marginTop: "12px" } },
        h("h3", { class: "muted", style: { marginBottom: "6px" } }, "Dry run proposal"),
        r.wouldIssue ? h("pre", {}, r.wouldIssue) : null,
        r.wouldPr ? h("pre", {}, r.wouldPr) : null,
      ) : null,
      r.lessons ? h("div", { style: { marginTop: "12px" } },
        h("h3", { class: "muted", style: { marginBottom: "6px" } }, "Rules"),
        h("pre", {}, r.lessons),
      ) : null,
      r.memory ? h("div", { style: { marginTop: "12px" } },
        h("h3", { class: "muted", style: { marginBottom: "6px" } }, "Facts"),
        h("pre", {}, r.memory),
      ) : null,
      !r.lessons && !r.memory && !r.wouldIssue && !r.wouldPr ? h("p", { class: "muted", style: { marginTop: "10px" } }, "No notes for this repository yet.") : null,
    )));
  }

  getJSON("/api/memory")
    .then((data) => { repos = data; render(); })
    .catch((err) => mount(list, h("div", { class: "empty" }, `Cannot read lessons and memory (${err.message}).`)));

  return { destroy() {} };
}
