import { h, mount } from "../dom.js";
import { getJSON } from "../api.js";
import { failed, skeleton } from "../state.js";
import { t } from "../i18n.js";

// What the daily pass learned per repository: rules it derives from reviews,
// and facts it keeps between passes.
export function mountMemory(el, arg = "") {
  const search = h("input", {
    type: "search",
    placeholder: t("common.filter_repositories"),
    value: arg,
    "aria-label": t("memory.filter_memory_by_repository"),
    oninput: () => { filter = search.value.trim().toLowerCase(); render(); },
  });
  const list = h("div", { class: "list" }, skeleton());
  let repos = [];
  let filter = arg.trim().toLowerCase();

  mount(el, h("div", { class: "stack" },
    h("div", { class: "row between" },
      h("div", {}, h("h1", {}, t("common.memory")), h("p", { class: "muted" }, t("memory.written_by_tend_repo_rules"))),
      search,
    ),
    list,
  ));

  function render() {
    const rows = repos.filter((r) => !filter || r.repo.toLowerCase().includes(filter));
    if (!rows.length) {
      mount(list, h("div", { class: "empty" }, repos.length ? t("common.nothing_matches") : t("memory.no_pass_has_written_lessons")));
      return;
    }
    mount(list, rows.map((r) => h("div", { class: "card" },
      h("div", { class: "row between" },
        h("a", { href: `https://github.com/${r.repo}`, target: "_blank", rel: "noreferrer" }, h("b", {}, r.repo)),
        r.since ? h("span", { class: "muted", style: { fontSize: "12px" } }, t("memory.covered_up_to", [r.since])) : null,
      ),
      r.wouldIssue || r.wouldPr ? h("details", { class: "fold" },
        h("summary", {}, t("memory.dry_run_proposal")),
        r.wouldIssue ? h("pre", {}, r.wouldIssue) : null,
        r.wouldPr ? h("pre", {}, r.wouldPr) : null,
      ) : null,
      r.lessons ? h("details", { class: "fold", open: true },
        h("summary", {}, t("memory.rules")),
        h("pre", {}, r.lessons),
      ) : null,
      r.memory ? h("details", { class: "fold" },
        h("summary", {}, t("memory.facts")),
        h("pre", {}, r.memory),
      ) : null,
      !r.lessons && !r.memory && !r.wouldIssue && !r.wouldPr ? h("p", { class: "muted", style: { marginTop: "10px" } }, t("memory.no_notes_for_this_repository")) : null,
    )));
  }

  getJSON("/api/memory")
    .then((data) => { repos = data; render(); })
    .catch((err) => mount(list, failed(t("memory.cannot_read_lessons_and_memory", [err.message]), () => mountMemory(el, arg))));

  return { destroy() {} };
}
