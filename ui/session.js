// Who is looking (ADR 0044, ADR 0046). The server decides what a visitor may read; this module only
// shapes the page around the answer: the sign-in page, the identity in the nav, and which
// sections a visitor is offered.
import { h, mount } from "./dom.js";
import { getJSON, postJSON } from "./api.js";
import { t } from "./i18n.js";

// Sections that read or change what only the host's own user may touch.
export const OWNER_ONLY = new Set(["activity", "workflows", "config", "repos", "health"]);

export const session = { enabled: false, role: "owner", login: "", minPermission: "push" };

export async function loadSession() {
  try {
    const s = await getJSON("/api/session");
    Object.assign(session, { enabled: !!s.enabled, role: s.role || "owner", login: s.login || "", minPermission: s.min_permission === "pull" ? "pull" : "push" });
  } catch {
    // 404: sign-in is off. Any other failure: leave the page as it always was.
  }
  return session;
}

export const isVisitor = () => session.role === "visitor";

// The nav of a visitor: owner-only sections go, and "signed in as" comes with a way out.
export function shapeNav() {
  if (!isVisitor()) return;
  document.querySelectorAll("#tabs a").forEach((a) => {
    if (OWNER_ONLY.has(a.dataset.view)) a.hidden = true;
  });
  document.querySelectorAll("#tabs .sep").forEach((e) => e.setAttribute("hidden", ""));
  document.querySelector("#awf-status")?.setAttribute("hidden", "");
  const who = document.querySelector("#whoami");
  if (!who) return;
  who.hidden = false;
  who.replaceChildren(
    h("span", { class: "muted", title: t("session.signed_in_with_github") }, h("span", { class: "who-prefix" }, t("session.signed_in_as")), session.login),
    h("button", { class: "btn ghost sm", type: "button", onclick: signOut }, t("session.sign_out")),
  );
}

async function signOut() {
  try { await postJSON("/auth/logout", {}); } catch { /* the session ends at expiry anyway */ }
  location.reload();
}

const safeLink = (u) => {
  try {
    const url = new URL(u);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : "";
  } catch { return ""; }
};

// The sign-in page: a code, a link to GitHub, and a poll until the visitor approves.
export function showSignIn(el) {
  const body = h("div", {});
  mount(el, h("div", { class: "signin" },
    h("h1", {}, t("session.sign_in")),
    h("p", { class: "muted" }, t("session.you_see_the_repositories_where", [session.minPermission === "pull" ? t("session.read") : t("session.push")])),
    body,
  ));
  let timer = null;
  const idle = (message) => {
    clearTimeout(timer);
    mount(body,
      message ? h("p", { class: "signin-note" }, message) : null,
      h("button", { class: "btn primary", type: "button", onclick: start }, t("session.sign_in_with_github")),
    );
  };

  async function start() {
    mount(body, h("p", { class: "muted" }, t("session.asking_github_for_a_code")));
    let flow;
    try {
      flow = await postJSON("/auth/device/start", {});
    } catch (err) {
      idle(err.status === 429 ? t("session.too_many_attempts_wait_a") : t("session.could_not_start_the_sign", [err.message]));
      return;
    }
    const link = safeLink(flow.verification_uri);
    mount(body,
      h("p", {}, t("session.open_github_and_enter_this")),
      h("p", { class: "signin-code", "aria-label": t("session.your_code") }, flow.user_code),
      link ? h("p", {}, h("a", { class: "btn", href: link, target: "_blank", rel: "noopener noreferrer" }, t("session.open_github"))) : null,
      h("p", { class: "muted", role: "status" }, t("session.waiting_for_you_to_approve")),
    );
    poll(flow.id, flow.interval || 5, Date.now() + (flow.expires_in || 900) * 1000);
  }

  function poll(id, seconds, until) {
    timer = setTimeout(async () => {
      if (Date.now() > until) return idle(t("session.the_code_expired_start_again"));
      try {
        const res = await postJSON("/auth/device/poll", { id });
        if (res.status === "ok") return location.reload();
        if (res.status === "expired") return idle(t("session.the_code_expired_start_again"));
        if (res.status === "denied") return idle(t("session.github_reported_that_the_request"));
        poll(id, res.interval || seconds, until);
      } catch (err) {
        if (err.status === 429) return poll(id, seconds + 5, until);
        idle(t("session.the_sign_in_failed", [err.message]));
      }
    }, seconds * 1000);
  }

  idle("");
}
