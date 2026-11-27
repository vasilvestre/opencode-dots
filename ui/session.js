// Who is looking (ADR 0044, ADR 0046). The server decides what a visitor may read; this module only
// shapes the page around the answer: the sign-in page, the identity in the nav, and which
// sections a visitor is offered.
import { h, mount } from "./dom.js";
import { getJSON, postJSON } from "./api.js";

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
  document.querySelector("#awf-status")?.setAttribute("hidden", "");
  const who = document.querySelector("#whoami");
  if (!who) return;
  who.hidden = false;
  who.replaceChildren(
    h("span", { class: "muted", title: "Signed in with GitHub" }, h("span", { class: "who-prefix" }, "signed in as "), session.login),
    h("button", { class: "btn ghost sm", type: "button", onclick: signOut }, "Sign out"),
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
    h("h1", {}, "Sign in"),
    h("p", { class: "muted" }, `You see the repositories where you can ${session.minPermission === "pull" ? "read" : "push"}. Signing in asks GitHub for the repo scope, the one the gh tool asks for. The dashboard uses your token once, to read who you are and your rights on the repositories it works on, then forgets it. Your session lasts one hour.`),
    body,
  ));
  let timer = null;
  const idle = (message) => {
    clearTimeout(timer);
    mount(body,
      message ? h("p", { class: "signin-note" }, message) : null,
      h("button", { class: "btn primary", type: "button", onclick: start }, "Sign in with GitHub"),
    );
  };

  async function start() {
    mount(body, h("p", { class: "muted" }, "Asking GitHub for a code..."));
    let flow;
    try {
      flow = await postJSON("/auth/device/start", {});
    } catch (err) {
      idle(err.status === 429 ? "Too many attempts. Wait a minute and try again." : `Could not start the sign-in: ${err.message}`);
      return;
    }
    const link = safeLink(flow.verification_uri);
    mount(body,
      h("p", {}, "Open GitHub and enter this code:"),
      h("p", { class: "signin-code", "aria-label": "Your code" }, flow.user_code),
      link ? h("p", {}, h("a", { class: "btn", href: link, target: "_blank", rel: "noopener noreferrer" }, "Open GitHub")) : null,
      h("p", { class: "muted", role: "status" }, "Waiting for you to approve..."),
    );
    poll(flow.id, flow.interval || 5, Date.now() + (flow.expires_in || 900) * 1000);
  }

  function poll(id, seconds, until) {
    timer = setTimeout(async () => {
      if (Date.now() > until) return idle("The code expired. Start again.");
      try {
        const res = await postJSON("/auth/device/poll", { id });
        if (res.status === "ok") return location.reload();
        if (res.status === "expired") return idle("The code expired. Start again.");
        if (res.status === "denied") return idle("GitHub reported that the request was declined.");
        poll(id, res.interval || seconds, until);
      } catch (err) {
        if (err.status === 429) return poll(id, seconds + 5, until);
        idle(`The sign-in failed: ${err.message}`);
      }
    }, seconds * 1000);
  }

  idle("");
}
