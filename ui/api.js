// One place that talks HTTP. Errors carry the status and the body so views can
// tell "awf is down" from "the file is invalid".

export class ApiError extends Error {
  constructor(message, status, body) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

async function readBody(res) {
  const text = await res.text();
  if (!text) return null;
  try { return JSON.parse(text); } catch { return text; }
}

export async function request(url, opts = {}) {
  const { timeout = 15000, ...init } = opts;
  let res;
  try {
    res = await fetch(url, {
      ...init,
      cache: "no-store",
      signal: init.signal || AbortSignal.timeout(timeout),
    });
  } catch (err) {
    throw new ApiError(err.name === "TimeoutError" ? "request timed out" : err.message, 0, null);
  }
  const body = await readBody(res);
  if (!res.ok) {
    const message = (body?.error) || (typeof body === "string" ? body : `HTTP ${res.status}`);
    throw new ApiError(message, res.status, body);
  }
  return body;
}

export const getJSON = (url, opts) => request(url, opts);

export async function getText(url, opts = {}) {
  const res = await fetch(url, { ...opts, cache: "no-store" });
  if (!res.ok) throw new ApiError(`HTTP ${res.status}`, res.status, await res.text());
  return res.text();
}

export const awf = (path, opts) => request(`/awf${path}`, opts);

export function putJSON(url, value) {
  return request(url, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(value),
  });
}

export function postJSON(url, value) {
  return request(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(value),
  });
}
