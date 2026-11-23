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

// richText renders one agent answer: bold, code spans and list markers. It
// stays on text nodes, so no answer ever becomes HTML. Anything else stays as
// written, which is why the agent is asked for plain prose with little markup.
const inlineRE = /(\*\*[^*\n]+\*\*|`[^`\n]+`)/g;

function inline(text) {
  const out = [];
  let last = 0;
  for (const m of text.matchAll(inlineRE)) {
    if (m.index > last) out.push(document.createTextNode(text.slice(last, m.index)));
    const tok = m[0];
    out.push(tok.startsWith("**") ? h("b", {}, tok.slice(2, -2)) : h("code", {}, tok.slice(1, -1)));
    last = m.index + tok.length;
  }
  if (last < text.length) out.push(document.createTextNode(text.slice(last)));
  return out;
}

export function richText(text) {
  const frag = document.createDocumentFragment();
  const lines = String(text).split("\n");
  lines.forEach((line, i) => {
    if (i) frag.appendChild(document.createTextNode("\n"));
    const li = /^(\s*(?:\d+\.|[-*]))\s+(.*)$/.exec(line);
    if (li) {
      frag.appendChild(document.createTextNode(`${li[1]} `));
      for (const node of inline(li[2])) frag.appendChild(node);
      return;
    }
    for (const node of inline(line)) frag.appendChild(node);
  });
  return frag;
}
