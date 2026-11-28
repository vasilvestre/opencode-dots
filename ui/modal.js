import { $, h } from "./dom.js";
import { t } from "./i18n.js";

let openCount = 0;

// openModal returns the root element plus a close function. Escape, backdrop
// click and the close button all call it; focus returns where it came from.
export function openModal({ title, body, actions = [], width, onClose }) {
  const previous = document.activeElement;
  const root = $("#modal-root");
  const backdrop = h("div", { class: "modal-backdrop" });

  const close = () => {
    backdrop.remove();
    openCount = Math.max(0, openCount - 1);
    document.body.style.overflow = openCount ? "hidden" : "";
    if (previous instanceof HTMLElement) previous.focus();
    onClose?.();
  };

  const dialog = h("div", {
    class: "modal",
    role: "dialog",
    "aria-modal": "true",
    "aria-label": title,
    style: width ? { width } : undefined,
    onclick: (ev) => ev.stopPropagation(),
  },
  h("header", {},
    h("h2", {}, title),
    h("button", { class: "btn ghost sm", "aria-label": t("modal.close_esc"), title: "Esc", onclick: close }, t("modal.close")),
  ),
  h("div", { class: "body" }, body),
  actions.length ? h("footer", {}, actions.map((a) =>
    h("button", {
      id: a.id,
      class: `btn ${a.class || ""}`,
      disabled: a.disabled,
      onclick: () => a.onClick?.(close),
    }, a.label))) : null,
  );

  backdrop.appendChild(dialog);
  backdrop.addEventListener("click", close);
  root.appendChild(backdrop);
  openCount += 1;
  document.body.style.overflow = "hidden";

  const onKey = (ev) => {
    if (ev.key === "Escape") { ev.preventDefault(); close(); }
    if (ev.key === "Tab") trapFocus(ev, dialog);
  };
  document.addEventListener("keydown", onKey, true);

  // close() removes the backdrop; drop the key listener at the same time.
  const originalRemove = backdrop.remove.bind(backdrop);
  backdrop.remove = () => { document.removeEventListener("keydown", onKey, true); originalRemove(); };
  backdrop.close = close;

  queueMicrotask(() => {
    const target = dialog.querySelector("input, textarea, select, button.primary") || dialog;
    target.focus();
  });
  return backdrop;
}

function trapFocus(ev, dialog) {
  const items = [...dialog.querySelectorAll('a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])')]
    .filter((el) => el.offsetParent !== null);
  if (!items.length) return;
  const first = items[0];
  const last = items[items.length - 1];
  if (ev.shiftKey && document.activeElement === first) { ev.preventDefault(); last.focus(); }
  else if (!ev.shiftKey && document.activeElement === last) { ev.preventDefault(); first.focus(); }
}

export function confirmDialog({ title, message, confirm = t("modal.confirm"), danger = false }) {
  return new Promise((resolve) => {
    let answered = false;
    const modal = openModal({
      title,
      body: h("p", {}, message),
      actions: [
        { label: t("common.cancel"), class: "ghost", onClick: (close) => close() },
        {
          label: confirm,
          class: danger ? "danger" : "primary",
          onClick: (close) => { answered = true; resolve(true); close(); },
        },
      ],
      onClose: () => { if (!answered) resolve(false); },
    });
    return modal;
  });
}
