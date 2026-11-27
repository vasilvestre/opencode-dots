import { h, mount } from "./dom.js";
import { awf, getJSON } from "./api.js";
import { openModal } from "./modal.js";

const SECTIONS = [
  ["Home", "home"],
  ["Activity", "activity"],
  ["Workflows", "workflows"],
  ["Settings", "config"],
  ["Repos", "repos"],
  ["Cost", "costs"],
  ["Memory", "memory"],
  ["Autonomy", "autonomy"],
  ["Health", "health"],
  ["Audit", "audit"],
  ["Links", "links"],
];

// Ctrl+K: jump to a section, a workflow or a config file by typing a few
// letters. Targets are plain hashes, so the router does the rest.
export function openPalette() {
  let items = [
    ...SECTIONS.map(([label, view]) => ({ label, hint: "section", href: `#${view}` })),
    { label: "Model defaults", hint: "settings", href: "#config/models" },
    { label: "Notifications", hint: "settings", href: "#config/notifications" },
    { label: "Appearance", hint: "settings", href: "#config/appearance" },
  ];
  let active = 0;
  let shown = [];

  const input = h("input", {
    type: "text",
    class: "palette-input",
    placeholder: "Go to a section, workflow or config file",
    "aria-label": "Go to",
    autocomplete: "off",
    oninput: () => { active = 0; paint(); },
  });
  const list = h("div", { class: "palette-list", role: "listbox" });
  const modal = openModal({ title: "Go to", width: "560px", body: h("div", {}, input, list) });

  function go(item) {
    modal.close();
    location.hash = item.href;
  }

  function paint() {
    const q = input.value.trim().toLowerCase();
    shown = items.filter((i) => !q || i.label.toLowerCase().includes(q)).slice(0, 30);
    active = Math.min(active, Math.max(0, shown.length - 1));
    mount(list, shown.length
      ? shown.map((item, i) => h("button", {
        class: "palette-item",
        role: "option",
        "aria-selected": String(i === active),
        onclick: () => go(item),
        onmousemove: () => { if (active !== i) { active = i; paint(); } },
      }, h("span", {}, item.label), h("span", { class: "muted" }, item.hint)))
      : h("div", { class: "empty" }, "Nothing matches."));
    list.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "ArrowDown") { ev.preventDefault(); active = Math.min(shown.length - 1, active + 1); paint(); }
    else if (ev.key === "ArrowUp") { ev.preventDefault(); active = Math.max(0, active - 1); paint(); }
    else if (ev.key === "Enter" && shown[active]) { ev.preventDefault(); go(shown[active]); }
  });

  paint();

  // Workflows and files arrive later; the palette is usable meanwhile.
  awf("/api/workflows").then((res) => {
    const wf = (res.body?.workflows || []).map((w) => ({
      label: w.name, hint: "workflow", href: `#workflows/${encodeURIComponent(w.name)}`,
    }));
    items = [...items, ...wf];
    paint();
  }).catch(() => {});
  getJSON("/api/files").then((files) => {
    const cfg = files.filter((f) => f.kind !== "workflow").map((f) => ({
      label: f.path, hint: "settings file", href: `#config/${f.path}`,
    }));
    items = [...items, ...cfg];
    paint();
  }).catch(() => {});
}
