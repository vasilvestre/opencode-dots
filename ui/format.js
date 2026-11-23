export function ago(iso) {
  if (!iso) return "unknown";
  const diff = Date.now() - Date.parse(iso);
  if (!Number.isFinite(diff)) return "unknown";
  const s = Math.max(0, Math.round(diff / 1000));
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
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
  return Number.isNaN(d.getTime()) ? "unknown" : d.toLocaleString();
}

export function elapsed(fromISO) {
  return dur(Date.now() - Date.parse(fromISO || ""));
}

export function bytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// awf streams one `data: {...}` line per event. Keep the log readable: seq,
// step, event kind, and any prompt or error text.
export function formatLog(raw) {
  const lines = [];
  for (const line of String(raw).split("\n")) {
    if (!line.startsWith("data: ")) continue;
    let e;
    try { e = JSON.parse(line.slice(6)); } catch { continue; }
    let text = `${String(e.seq ?? "").padStart(3)} ${e.step || "-"} ${e.kind || ""}`;
    if (e.status) text += ` (${e.status})`;
    if (e.error) text += ` error: ${e.error}`;
    if (e.prompt) text += `\n    ${String(e.prompt).slice(0, 300).replace(/\n/g, "\n    ")}`;
    lines.push(text);
  }
  return lines.join("\n");
}
