// Desktop notifications for finished workflow runs. The dashboard owns the
// Discord webhook; this module only owns the browser-side permission, the on/off
// choice and the run ids it has already announced.

import { awf } from "./api.js";
import { t, locale } from "./i18n.js";

const DESKTOP_KEY = "dots-desktop-notifications";
const SEEN_KEY = "dots-notified-runs";
const MAX_SEEN = 200;
const POLL_MS = 10000;
const TERMINAL = new Set(["completed", "failed"]);

export function desktopState() {
  if (typeof Notification === "undefined") return "unsupported";
  return Notification.permission;
}

export function isDesktopEnabled() {
  return localStorage.getItem(DESKTOP_KEY) === "on";
}

export function setDesktopEnabled(on) {
  localStorage.setItem(DESKTOP_KEY, on ? "on" : "off");
}

export function requestDesktopPermission() {
  if (typeof Notification === "undefined") return Promise.resolve("unsupported");
  return Notification.requestPermission();
}

function loadSeen() {
  try {
    const ids = JSON.parse(localStorage.getItem(SEEN_KEY) || "[]");
    return new Set(Array.isArray(ids) ? ids.slice(-MAX_SEEN) : []);
  } catch {
    return new Set();
  }
}

export function startNotifications() {
  const seen = loadSeen();
  let seeded = false;

  const saveSeen = () => {
    try {
      localStorage.setItem(SEEN_KEY, JSON.stringify([...seen].slice(-MAX_SEEN)));
    } catch {
      // Storage may be full or blocked; the in-memory set still prevents repeats.
    }
  };

  const poll = async () => {
    let entries;
    try {
      const res = await awf("/api/history?limit=20");
      entries = res?.body?.entries || [];
    } catch {
      return; // awf is down; the next tick retries.
    }
    const terminal = entries.filter((e) => e && TERMINAL.has(e.status));

    // The first good read is the baseline: runs already in history are old.
    if (!seeded) {
      seeded = true;
      for (const e of terminal) if (e.id) seen.add(e.id);
      if (terminal.length) saveSeen();
      return;
    }

    const fresh = [];
    for (const e of terminal) {
      if (!e.id || seen.has(e.id)) continue;
      seen.add(e.id);
      fresh.push(e);
    }
    if (!fresh.length) return;
    saveSeen();

    if (!isDesktopEnabled() || desktopState() !== "granted") return;
    for (const e of fresh) {
      try {
        new Notification(notificationTitle(e), { body: notificationBody(e), tag: e.id });
      } catch {
        // A browser may refuse a notification without a user gesture. The id is
        // already seen, so the next tick does not try it again.
      }
    }
  };

  poll();
  setInterval(poll, POLL_MS);
}

function notificationTitle(e) {
  return `${e.workflow_name || "Workflow"} ${e.status}`;
}

function notificationBody(e) {
  const at = e.completed_at || e.started_at;
  const time = at && !Number.isNaN(Date.parse(at))
    ? new Date(at).toLocaleTimeString(locale())
    : t("notifications.just_now");
  return t("notifications.run_finished_at", [String(e.id).slice(0, 8), time]);
}
