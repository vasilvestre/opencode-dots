import { h } from "./dom.js";
import { stepLabel } from "./steps.js";
import { t } from "./i18n.js";

// A workflow drawn as a diagram: one box per step, one arrow per transition,
// laid out in rows from the initial step downwards. Everything comes from the
// awf workflow detail; nothing here guesses beyond layout.
//
// Edge kinds:
//   success    OnSuccess, or the implicit next step of a loop body
//   failure    OnFailure, drawn only when it leads somewhere that is not an
//              error terminal (those are folded into a red marker on the node)
//   branch     a Transitions entry: "when <condition> go to <step>"
//   loop       a loop container entering its body
//   back       any edge that returns to an earlier step (a retry or next round)

const NODE_W = 184;
const NODE_H = 46;
const PITCH_X = 214;
const PITCH_Y = 76;
const PAD = 24;
const SVG_NS = "http://www.w3.org/2000/svg";

function svg(tag, attrs = {}, ...kids) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) if (v != null && v !== false) el.setAttribute(k, v);
  for (const kid of kids) if (kid != null) el.append(kid);
  return el;
}

export function renderFlow(w, { onSelect, compact = false } = {}) {
  const steps = w.Steps || {};
  if (!Object.keys(steps).length) return h("div", { class: "empty" }, t("flow.this_workflow_has_no_steps"));
  if (!steps[w.Initial]) {
    return h("div", { class: "empty" }, t("flow.the_initial_step_is_not", [w.Initial || ""]));
  }

  const model = buildModel(w, steps);
  const layout = layoutModel(model, w.Initial);
  const wfName = w.Name || "";

  let selected = w.Initial;
  const detail = h("aside", { class: "flow-detail", "aria-live": "polite" });
  const canvas = h("div", { class: "flow-canvas" });
  const root = h("div", { class: compact ? "flow compact" : "flow" }, canvas, detail);

  const diagram = draw(layout, wfName, (name) => select(name));
  canvas.append(diagram.el);

  function select(name, { silent = false } = {}) {
    if (!model.nodes.has(name)) return;
    selected = name;
    if (!silent) onSelect?.(name);
    diagram.highlight(name);
    paintDetail();
  }

  function paintDetail() {
    const n = model.nodes.get(selected);
    detail.replaceChildren(...detailNodes(n, model, wfName, select).filter(Boolean));
  }

  select(w.Initial, { silent: true });

  const out = h("div", { class: "stack" },
    h("div", { class: "file-meta" },
      h("span", {}, t("flow.steps", [model.nodes.size])),
      h("span", {}, t("flow.initial", [w.Initial])),
      w.Version ? h("span", {}, t("common.version", [w.Version])) : null,
    ),
    w.Description ? h("p", { class: "muted" }, w.Description) : null,
    legend(model),
    root,
  );
  out.selectStep = (name) => select(name, { silent: true });
  return out;
}

// --- model ------------------------------------------------------------------

function buildModel(w, steps) {
  const nodes = new Map();
  const loopOf = new Map(); // body step -> container name
  for (const [name, s] of Object.entries(steps)) {
    for (const b of s.Loop?.Body || []) loopOf.set(b, name);
  }
  for (const [name, s] of Object.entries(steps)) {
    nodes.set(name, { name, step: s, kind: s.Type || "step", loop: loopOf.get(name) || "", out: [], failsTo: [] });
  }

  const add = (from, kind, to, label = "") => {
    if (!to || !nodes.has(to) || to === from) return;
    const n = nodes.get(from);
    const dup = n.out.find((e) => e.to === to && e.kind === kind);
    if (dup) {
      if (label && !dup.label.split(" / ").includes(label)) dup.label = dup.label ? `${dup.label} / ${label}` : label;
      return;
    }
    n.out.push({ kind, to, label });
  };

  for (const [name, s] of Object.entries(steps)) {
    const container = loopOf.get(name);
    if (container) {
      const body = steps[container].Loop.Body;
      const next = body[body.indexOf(name) + 1];
      add(name, "success", s.OnSuccess && s.OnSuccess !== container ? s.OnSuccess : next);
    } else {
      add(name, "success", s.OnSuccess);
    }
    if (s.Loop) {
      add(name, "loop", s.Loop.Body?.[0], t("flow.each_round_up_to", [s.Loop.MaxIterations || "?"]));
      add(name, "success", s.Loop.OnComplete, t("flow.when_the_loop_ends"));
    }
    for (const tr of s.Transitions || []) add(name, "branch", tr.Goto, conditionLabel(tr.When));
    for (const b of s.Branches || []) add(name, "branch", b.Next, conditionLabel(b.Condition || b.When || b.Name));
    add(name, "failure", s.OnFailure);
  }

  // A terminal reached only through failure edges is noise: fold it.
  const incoming = new Map();
  for (const n of nodes.values()) for (const e of n.out) {
    (incoming.get(e.to) || incoming.set(e.to, []).get(e.to)).push(e.kind);
  }
  const folded = new Set();
  for (const n of nodes.values()) {
    const kinds = incoming.get(n.name) || [];
    if (n.kind === "terminal" && kinds.length && kinds.every((k) => k === "failure")) folded.add(n.name);
  }
  for (const n of nodes.values()) {
    n.failsTo = n.out.filter((e) => e.kind === "failure" && folded.has(e.to)).map((e) => e.to);
    n.out = n.out.filter((e) => !folded.has(e.to));
  }
  for (const f of folded) nodes.get(f).folded = true;
  return { nodes, folded };
}

function conditionLabel(when) {
  if (!when) return "";
  const m = String(when).match(/contains\s+"([^"]+)"/);
  if (m) return m[1];
  const s = String(when).replace(/\s+/g, " ").trim();
  return s.length > 28 ? `${s.slice(0, 27)}…` : s;
}

// --- layout -----------------------------------------------------------------

function layoutModel(model, initial) {
  const visible = [...model.nodes.values()].filter((n) => !n.folded);
  const byName = new Map(visible.map((n) => [n.name, n]));

  // DFS from the initial step: reverse post-order and the edges that return to
  // a step still on the stack (back edges).
  const post = [];
  const seen = new Set();
  const onStack = new Set();
  const back = new Set();
  const dfs = (name) => {
    seen.add(name);
    onStack.add(name);
    for (const e of byName.get(name).out) {
      if (!byName.has(e.to)) continue;
      if (onStack.has(e.to)) back.add(`${name}>${e.to}>${e.kind}`);
      else if (!seen.has(e.to)) dfs(e.to);
    }
    onStack.delete(name);
    post.push(name);
  };
  if (byName.has(initial)) dfs(initial);
  for (const n of visible) if (!seen.has(n.name)) dfs(n.name);
  const order = post.reverse();

  const isBack = (from, e) => back.has(`${from}>${e.to}>${e.kind}`);
  const rank = new Map(order.map((n) => [n, 0]));
  for (const name of order) {
    for (const e of byName.get(name).out) {
      if (!byName.has(e.to) || isBack(name, e)) continue;
      rank.set(e.to, Math.max(rank.get(e.to), rank.get(name) + 1));
    }
  }

  const rows = [];
  for (const name of order) (rows[rank.get(name)] ||= []).push(name);
  const preds = new Map();
  for (const n of visible) for (const e of n.out) {
    if (byName.has(e.to) && !isBack(n.name, e)) (preds.get(e.to) || preds.set(e.to, []).get(e.to)).push(n.name);
  }

  const maxCols = Math.max(...rows.map((r) => r?.length || 0));
  const pos = new Map();
  const place = () => rows.forEach((row, r) => {
    row?.forEach((name, i) => pos.set(name, { r, i, n: row.length }));
  });
  place();
  // Barycenter passes: order each row by where its predecessors sit.
  for (let pass = 0; pass < 3; pass++) {
    rows.forEach((row) => {
      if (!row || row.length < 2) return;
      const bary = (name) => {
        const ps = preds.get(name) || [];
        if (!ps.length) return pos.get(name).i;
        return ps.reduce((a, p) => a + centerX(pos.get(p)), 0) / ps.length;
      };
      row.sort((a, b) => bary(a) - bary(b));
    });
    place();
  }

  const backCount = back.size;
  const width = maxCols * PITCH_X + PAD * 2 + backCount * 14 + 40;
  const height = rows.length * PITCH_Y + PAD * 2;
  const coords = new Map();
  const cx = PAD + (maxCols * PITCH_X) / 2;
  for (const [name, p] of pos) {
    coords.set(name, { x: cx + centerX(p) - NODE_W / 2, y: PAD + p.r * PITCH_Y });
  }

  return { visible, byName, coords, width, height, isBack, maxCols, rows };
}

function centerX(p) {
  return (p.i - (p.n - 1) / 2) * PITCH_X;
}

// --- drawing ----------------------------------------------------------------

function draw(layout, wfName, onSelect) {
  const { visible, byName, coords, width, height, isBack, maxCols } = layout;
  const root = svg("svg", {
    class: "flow-svg",
    viewBox: `0 0 ${width} ${height}`,
    width,
    role: "group",
    "aria-label": t("flow.workflow_diagram"),
  });

  const defs = svg("defs");
  for (const kind of ["success", "failure", "branch", "loop", "back"]) {
    defs.append(svg("marker", {
      id: `arrow-${kind}`, viewBox: "0 0 10 10", refX: "9", refY: "5",
      markerWidth: "7", markerHeight: "7", orient: "auto-start-reverse",
    }, svg("path", { d: "M0 0 L10 5 L0 10 z", class: `arrow ${kind}` })));
  }
  root.append(defs);

  const edgeLayer = svg("g", { class: "edges" });
  const nodeLayer = svg("g", { class: "nodes" });
  root.append(edgeLayer, nodeLayer);

  const railBase = PAD + maxCols * PITCH_X + 6;
  let rail = 0;
  const edgeEls = [];
  for (const n of visible) {
    const a = coords.get(n.name);
    for (const e of n.out) {
      if (!byName.has(e.to)) continue;
      const b = coords.get(e.to);
      const backward = isBack(n.name, e) || b.y <= a.y;
      const skip = !backward && b.y - a.y > PITCH_Y * 1.5;
      let d;
      let cls = e.kind;
      let lx;
      let ly;
      if (backward || skip) {
        if (backward) cls = "back";
        const x = railBase + 14 * rail++;
        const sx = a.x + NODE_W;
        const sy = a.y + NODE_H / 2;
        const ty = b.y + NODE_H / 2;
        d = `M${sx} ${sy} H${x} V${ty} H${b.x + NODE_W}`;
        lx = x;
        ly = (sy + ty) / 2;
      } else {
        const sx = a.x + NODE_W / 2;
        const sy = a.y + NODE_H;
        const tx = b.x + NODE_W / 2;
        const ty = b.y;
        const mid = (sy + ty) / 2;
        d = `M${sx} ${sy} C${sx} ${mid} ${tx} ${mid} ${tx} ${ty}`;
        lx = (sx + tx) / 2;
        ly = mid;
      }
      const path = svg("path", { d, class: `edge-path ${cls}`, "marker-end": `url(#arrow-${cls})` });
      const g = svg("g", { class: "edge-g", "data-from": n.name, "data-to": e.to }, path);
      const label = e.kind === "branch" || ((backward || skip) && e.label) ? e.label : "";
      if (label) {
        const text = svg("text", { x: lx, y: ly, class: `edge-label ${cls}`, "text-anchor": backward || skip ? "start" : "middle", dy: "0.32em" });
        text.textContent = label;
        g.append(text);
      }
      edgeLayer.append(g);
      edgeEls.push(g);
    }
  }

  const fullWidth = Math.max(width, railBase + 14 * rail + 90);
  root.setAttribute("viewBox", `0 0 ${fullWidth} ${height}`);
  root.setAttribute("width", fullWidth);

  const nodeEls = new Map();
  for (const n of visible) {
    const { x, y } = coords.get(n.name);
    const label = stepLabel(wfName, n.name);
    const title = label || n.name;
    const sub = nodeSub(n);
    const g = svg("g", {
      class: `fnode ${n.kind}${n.loop ? " in-loop" : ""}`,
      transform: `translate(${x} ${y})`,
      tabindex: "0",
      role: "button",
      "aria-label": t("flow.step", [title, n.kind]),
    });
    g.append(svg("rect", { width: NODE_W, height: NODE_H, rx: 9 }));
    const titleEl = svg("text", { x: 12, y: sub ? 19 : 28, class: "fn-title" });
    titleEl.textContent = truncate(title, 24);
    g.append(titleEl);
    if (sub) {
      const s = svg("text", { x: 12, y: 35, class: "fn-sub" });
      s.textContent = truncate(sub, 30);
      g.append(s);
    }
    if (n.failsTo.length) {
      const dot = svg("circle", { cx: NODE_W - 12, cy: 12, r: 4, class: "fail-dot" });
      dot.append(svg("title", {}, t("flow.on_failure", [n.failsTo.join(", ")])));
      g.append(dot);
    }
    if (n.kind === "while" || n.kind === "for_each") {
      const c = svg("text", { x: NODE_W - 12, y: 31, class: "fn-loop", "text-anchor": "end" });
      c.textContent = "↻";
      g.append(c);
    }
    g.addEventListener("click", () => onSelect(n.name));
    g.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); onSelect(n.name); }
    });
    nodeLayer.append(g);
    nodeEls.set(n.name, g);
  }

  return {
    el: root,
    highlight(name) {
      for (const [k, g] of nodeEls) g.classList.toggle("selected", k === name);
      for (const g of edgeEls) {
        const hot = g.dataset.from === name || g.dataset.to === name;
        g.classList.toggle("hot", hot);
        g.classList.toggle("dim", !hot);
      }
    },
  };
}

function nodeSub(n) {
  const s = n.step;
  if (s.Agent) {
    const model = s.Agent.Options?.model;
    return model && !String(model).includes("{{") ? t("flow.agent", [model]) : "agent";
  }
  if (n.kind === "terminal") return s.Status ? String(s.Status) : "end";
  if (n.kind === "while" || n.kind === "for_each") return t("flow.loop_steps", [s.Loop?.Body?.length || 0]);
  return n.loop ? `in ${n.loop}` : "";
}

function truncate(s, n) {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

// --- detail panel -------------------------------------------------------------

function detailNodes(n, model, wfName, select) {
  const s = n.step;
  const label = stepLabel(wfName, n.name);
  const edge = (e) => h("li", {},
    h("span", { class: `edge ${e.kind}` },
      e.kind === "success" ? t("flow.success") : e.kind === "failure" ? t("flow.failure") : e.kind === "loop" ? t("flow.loop") : `branch${e.label ? ` ${e.label}` : ""}`),
    " → ",
    h("button", { class: "link", onclick: () => select(e.to) }, e.to),
    e.kind !== "branch" && e.label ? h("span", { class: "muted" }, ` ${e.label}`) : null,
  );

  const kv = [];
  if (s.Agent?.Provider) kv.push(["agent", s.Agent.Provider]);
  if (s.Agent?.Options?.model) kv.push(["model", s.Agent.Options.model]);
  if (n.kind === "command") kv.push(["runs", t("flow.shell_command")]);
  if (s.Timeout) kv.push(["timeout", `${s.Timeout}s`]);
  if (n.loop) kv.push([t("flow.inside_loop"), n.loop]);
  if (s.Loop) {
    kv.push([t("flow.max_rounds"), String(s.Loop.MaxIterations || "")]);
    if (s.Loop.BreakCondition) kv.push([t("flow.stops_when"), conditionLabel(s.Loop.BreakCondition)]);
    kv.push(["body", (s.Loop.Body || []).join(" → ")]);
  }

  const code = s.Command || s.Agent?.Prompt || "";
  return [
    h("div", { class: "row" },
      h("h3", {}, label || n.name),
      h("span", { class: "badge" }, n.kind)),
    label && label !== n.name ? h("div", { class: "muted mono" }, n.name) : null,
    s.Description && s.Description !== "null" ? h("p", { class: "muted" }, s.Description) : null,
    kv.length
      ? h("dl", { class: "flow-kv" }, kv.map(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]))
      : null,
    n.out.length || n.failsTo.length
      ? h("div", {},
        h("h4", {}, t("flow.goes_to")),
        h("ul", { class: "flow-edges" },
          n.out.map(edge),
          n.failsTo.map((to) => edge({ kind: "failure", to, label: t("flow.ends_the_run") }))))
      : h("p", { class: "muted" }, t("flow.ends_the_run_here")),
    code
      ? h("details", {},
        h("summary", {}, s.Agent ? t("common.prompt") : t("flow.command")),
        h("pre", {}, code.length > 1800 ? `${code.slice(0, 1800)}\n…` : code))
      : null,
  ];
}

function legend() {
  const item = (cls, text) => h("span", { class: "lg" }, h("i", { class: `lg-line ${cls}` }), text);
  return h("div", { class: "flow-legend" },
    item("success", "success"),
    item("failure", "failure"),
    item("branch", "condition"),
    item("back", t("flow.returns_to_an_earlier_step")),
    h("span", { class: "lg" }, h("i", { class: "fail-dot-lg" }), t("flow.fails_to_an_error_end")),
  );
}
