import { h } from "./dom.js";

// A workflow is a chain, not a grid: you almost always follow OnSuccess from
// the initial step. The view draws that chain in order and states every edge
// on its step, so a failure branch never looks like a parallel path.
export function renderFlow(w) {
  const steps = w.Steps || {};
  const names = Object.keys(steps);
  if (!names.length) return h("div", { class: "empty" }, "This workflow has no steps.");

  const spine = followSpine(steps, w.Initial);
  const rest = names.filter((n) => !spine.includes(n)).sort();

  return h("div", { class: "stack" },
    h("div", { class: "file-meta" },
      h("span", {}, `${names.length} step${names.length === 1 ? "" : "s"}`),
      h("span", {}, `initial: ${w.Initial || "none"}`),
      w.Version ? h("span", {}, `version ${w.Version}`) : null,
    ),
    w.Description ? h("p", { class: "muted" }, w.Description) : null,
    spine.length
      ? h("ol", { class: "spine", "aria-label": "Success path" },
        spine.map((name, i) => h("li", { class: "spine-item" }, stepCard(name, steps[name], {
          initial: name === w.Initial,
          index: i + 1,
          steps,
        }))))
      : h("div", { class: "empty" }, `The initial step ${w.Initial || ""} is not in Steps, so there is no chain to draw.`),
    rest.length
      ? h("div", { class: "stack", style: { marginTop: "18px" } },
        h("h3", { class: "muted" }, "Off the success path"),
        rest.map((name) => stepCard(name, steps[name], { initial: false, steps })),
      )
      : null,
  );
}

function followSpine(steps, initial) {
  const seen = new Set();
  const chain = [];
  let cur = initial;
  while (cur && steps[cur] && !seen.has(cur)) {
    seen.add(cur);
    chain.push(cur);
    cur = steps[cur].OnSuccess;
  }
  return chain;
}

function edgesOf(step, steps) {
  const out = [];
  if (step.OnSuccess && steps[step.OnSuccess]) out.push({ kind: "success", to: step.OnSuccess });
  if (step.OnFailure && steps[step.OnFailure]) out.push({ kind: "failure", to: step.OnFailure });
  for (const b of step.Branches || []) {
    if (b.Next && steps[b.Next]) out.push({ kind: "branch", to: b.Next, when: b.Condition || b.When || b.Name || "" });
  }
  return out;
}

function stepCard(name, step, { initial, index, steps }) {
  const kind = step.Type || "step";
  const terminal = kind === "terminal";
  const edges = edgesOf(step, steps);
  const summary = step.Agent
    ? [step.Agent.Provider, step.Agent.Options?.model].filter(Boolean).join(" · ")
    : step.Description || (step.Command ? "shell command" : "");

  return h("div", {
    class: ["node", initial ? "initial" : "", terminal ? "terminal" : "", kind === "agent" ? "agent" : ""].filter(Boolean).join(" "),
  },
  h("div", { class: "n-head" },
    index ? h("span", { class: "n-index" }, index) : null,
    h("span", { class: "n-name" }, name),
    initial ? h("span", { class: "badge ok" }, "initial") : null,
    h("span", { class: "n-kind" }, kind),
  ),
  summary ? h("div", { class: "n-sub" }, h("code", {}, summary)) : null,
  edges.length
    ? h("div", { class: "n-edges" }, edges.map((e) => h("span", { class: `edge ${e.kind}` },
      e.kind === "success" ? "✓ success" : e.kind === "failure" ? "✗ failure" : `branch ${e.when}`.trim(),
      " → ",
      h("b", {}, e.to),
    )))
    : h("div", { class: "n-edges" }, h("span", { class: "edge end" }, "ends here")),
  );
}
