import { h } from "./dom.js";
import { t } from "./i18n.js";

// A failed load with a way out: the message and a Retry that runs the loader again.
export function failed(message, retry) {
  return h("div", { class: "empty" },
    h("p", {}, message),
    retry ? h("button", { class: "btn sm", type: "button", style: { marginTop: "10px" }, onclick: retry }, t("common.retry")) : null);
}

// Placeholder while a view loads: grey rows instead of the word "Loading".
export function skeleton(rows = 4) {
  return h("div", { class: "skeleton", "aria-busy": "true", "aria-label": t("state.loading") },
    Array.from({ length: rows }, () => h("i")));
}
