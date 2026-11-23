// Tiny DOM builder. Everything the page renders goes through h(), so no
// server or log string ever becomes HTML.

function append(el, kids) {
  for (const kid of kids) {
    if (kid == null || kid === false || kid === true) continue;
    if (Array.isArray(kid)) append(el, kid);
    else if (kid instanceof Node) el.appendChild(kid);
    else el.appendChild(document.createTextNode(String(kid)));
  }
}

export function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value == null || value === false) continue;
    if (key === "class") el.className = value;
    else if (key === "text") el.textContent = value;
    else if (key === "dataset") Object.assign(el.dataset, value);
    else if (key === "style") Object.assign(el.style, value);
    else if (key.startsWith("on") && typeof value === "function") {
      el.addEventListener(key.slice(2).toLowerCase(), value);
    } else if (key === "checked" || key === "value" || key === "disabled" || key === "hidden") {
      el[key] = value;
    } else if (key in el && typeof value !== "string") {
      el[key] = value;
    } else {
      el.setAttribute(key, value === true ? "" : value);
    }
  }
  append(el, kids);
  return el;
}

export function mount(el, ...kids) {
  el.replaceChildren();
  append(el, kids);
  return el;
}

export const $ = (sel, root = document) => root.querySelector(sel);
