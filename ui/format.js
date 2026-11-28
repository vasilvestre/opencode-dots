import { t, locale } from "./i18n.js";

export function ago(iso) {
  if (!iso) return t("common.unknown");
  const diff = Date.now() - Date.parse(iso);
  if (!Number.isFinite(diff)) return t("common.unknown");
  const s = Math.max(0, Math.round(diff / 1000));
  if (s < 5) return t("format.just_now");
  if (s < 60) return t("format.s_ago", [s]);
  const m = Math.floor(s / 60);
  if (m < 60) return t("format.m_ago", [m]);
  const h = Math.floor(m / 60);
  if (h < 24) return t("format.h_ago", [h]);
  return t("format.d_ago", [Math.floor(h / 24)]);
}

export function dur(ms) {
  const s = Math.max(0, Math.round((ms || 0) / 1000));
  if (s === 0) return "0s";
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

export function when(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? t("common.unknown") : d.toLocaleString(locale());
}

export function elapsed(fromISO) {
  return dur(Date.now() - Date.parse(fromISO || ""));
}

export function bytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// One log line for one event: seq, step, kind, status, then the text the step
// or the agent produced.
export function eventLine(e) {
  let text = `${String(e.seq ?? "").padStart(3)} ${e.step || "-"} ${e.kind || ""}`;
  if (e.status) text += ` (${e.status})`;
  if (e.error) text += ` error: ${e.error}`;
  // A prompt is context the workflow sent; keep it short. The agent's answer is
  // what the reader came for.
  const body = e.text || e.prompt;
  const limit = e.kind === "message.user" ? 160 : 600;
  if (body) text += `\n    ${String(body).slice(0, limit).replace(/\n/g, "\n    ")}`;
  return text;
}

// awf streams one `data: {...}` line per event.
export function formatLog(raw) {
  const lines = [];
  for (const line of String(raw).split("\n")) {
    if (!line.startsWith("data: ")) continue;
    try { lines.push(eventLine(JSON.parse(line.slice(6)))); } catch { }
  }
  return lines.join("\n");
}
