import { $, h } from "./dom.js";

export function toast(message, { tone = "info", timeout = 5000 } = {}) {
  const root = $("#toasts");
  if (!root) return;
  const el = h("div", { class: `toast ${tone}`, role: tone === "err" ? "alert" : "status" },
    h("span", { class: "msg" }, message),
    h("button", {
      class: "btn ghost sm",
      "aria-label": "Dismiss",
      onclick: () => el.remove(),
    }, "×"),
  );
  root.appendChild(el);
  if (timeout > 0) setTimeout(() => el.remove(), timeout);
  return el;
}
