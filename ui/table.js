// Click a column header to sort any table. One MutationObserver on the view
// enhances every table a view mounts, so no view has to opt in.
// ponytail: sorts the text of the cell; "5m ago" sorts as text, give the cell a data-sort to override.

const num = (s) => (/^[$€]?-?[\d.,]+%?$/.test(s) ? parseFloat(s.replace(/[$€%,]/g, "")) : NaN);

function sortBy(table, col, dir) {
  const body = table.tBodies[0];
  if (!body) return;
  const key = (tr) => tr.cells[col]?.dataset.sort ?? tr.cells[col]?.textContent.trim() ?? "";
  const rows = [...body.rows].sort((a, b) => {
    const x = key(a), y = key(b);
    const nx = num(x), ny = num(y);
    const c = Number.isNaN(nx) || Number.isNaN(ny) ? x.localeCompare(y, undefined, { numeric: true }) : nx - ny;
    return c * dir;
  });
  body.append(...rows);
}

function enhance(table) {
  if (table.dataset.sortable || !table.tHead || !table.tBodies[0] || table.tBodies[0].rows.length < 2) return;
  table.dataset.sortable = "1";
  [...table.tHead.rows[0].cells].forEach((th, col) => {
    if (!th.textContent.trim()) return;
    th.classList.add("sortable");
    th.tabIndex = 0;
    const go = () => {
      const dir = th.getAttribute("aria-sort") === "ascending" ? -1 : 1;
      table.querySelectorAll("th[aria-sort]").forEach((o) => o.removeAttribute("aria-sort"));
      th.setAttribute("aria-sort", dir === 1 ? "ascending" : "descending");
      sortBy(table, col, dir);
    };
    th.addEventListener("click", go);
    th.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); go(); } });
  });
}

export function sortableTables(root) {
  const scan = () => root.querySelectorAll("table").forEach(enhance);
  new MutationObserver(scan).observe(root, { childList: true, subtree: true });
  scan();
}
