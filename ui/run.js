import { h, mount } from "./dom.js";
import { awf, getJSON } from "./api.js";
import { toast } from "./toast.js";
import { openModal } from "./modal.js";

// Collects the inputs of a workflow and starts it. Used by the editor and by
// the Replay action on a finished run, so both start a run the same way and
// both leave the same record behind for the next replay.
export function openRunModal({ scope, name, inputs, prefill = {}, note = "", onStarted }) {
  let closeFn = () => {};
  const repoList = h("datalist", { id: "run-repos" });
  const isRepo = (input) => /^repos?(itory)?$/i.test(input.Name);
  const wantsRepos = inputs.some((input) => isRepo(input));

  const fields = inputs.map((input) => {
    const id = `run-${input.Name}`;
    const type = String(input.Type || "string").toLowerCase();
    const rules = input.Validation || {};
    const current = prefill[input.Name] ?? input.Default ?? "";
    const options = type === "bool" || type === "boolean"
      ? [["true", "true"], ["false", "false"]]
      : Array.isArray(rules.Enum) && rules.Enum.length
        ? rules.Enum.map((v) => [v, v])
        : null;
    let control;
    if (options) {
      control = h("select", { id },
        input.Required ? null : h("option", { value: "", selected: current === "" }, "not set"),
        options.map(([value, label]) => h("option", {
          value,
          selected: String(current) === value,
        }, label)),
      );
    } else if (type === "integer" || type === "number") {
      control = h("input", {
        id,
        type: "number",
        step: "1",
        value: current,
        min: rules.Min ?? null,
        max: rules.Max ?? null,
      });
    } else {
      control = h("input", {
        id,
        type: "text",
        value: current,
        placeholder: isRepo(input) ? "owner/repo" : (rules.Pattern || ""),
        autocomplete: "off",
      });
    }
    if (isRepo(input) && control.tagName === "INPUT") control.setAttribute("list", "run-repos");
    return { input, control, type };
  });

  const form = h("form", {
    id: "run-form",
    class: "stack",
    style: { gap: "14px" },
    onsubmit: (ev) => {
      ev.preventDefault();
      submit(closeFn);
    },
  },
  h("div", { class: "warning" },
    h("span", {}, "This starts a real run. It spends an agent pass and may post, push or comment, depending on the workflow."),
  ),
  note ? h("p", { class: "hint" }, note) : null,
  fields.length
    ? fields.map(({ input, control }) => h("div", { class: "field" },
      h("label", { for: `run-${input.Name}` },
        input.Name,
        input.Required ? h("span", { class: "req" }, " *") : null,
        input.Type ? h("span", { class: "hint" }, ` (${input.Type})`) : null,
      ),
      control,
      input.Description ? h("span", { class: "hint" }, input.Description) : null,
    ))
    : h("p", { class: "muted" }, "This workflow takes no inputs."),
  repoList,
  );

  if (wantsRepos) {
    getJSON("/api/repos/names")
      .then((names) => mount(repoList, names.map((n) => h("option", { value: n }))))
      .catch(() => {});
  }

  const submit = async (close) => {
    const values = {};
    for (const { input, control, type } of fields) {
      const raw = control.value;
      if (raw === "" || raw == null) {
        if (input.Required) {
          toast(`${input.Name} is required`, { tone: "err" });
          control.focus();
          return;
        }
        continue;
      }
      if (type === "bool" || type === "boolean") values[input.Name] = raw === "true";
      else if (type === "integer" || type === "number") values[input.Name] = Number(raw);
      else values[input.Name] = raw;
    }
    try {
      const res = await awf(`/api/workflows/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/run`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ inputs: values }),
        timeout: 30000,
      });
      const id = res.body?.execution_id || "";
      rememberRun(id, { scope, name, inputs: values });
      close();
      onStarted(id);
    } catch (err) {
      const detail = err.body?.error || err.message;
      toast(`Run refused: ${detail}`, { tone: "err", timeout: 9000 });
    }
  };

  const modal = openModal({
    title: `Run ${name}`,
    body: form,
    actions: [
      { label: "Cancel", class: "ghost", onClick: (close) => close() },
      { label: "Run now", class: "primary", id: "run-submit", onClick: submit },
    ],
  });
  closeFn = () => modal.close();

  const primary = modal.querySelector("#run-submit");
  const missing = () => fields.some(({ input, control }) => input.Required && control.value === "");
  const sync = () => { primary.disabled = missing(); };
  for (const { control } of fields) {
    control.addEventListener("input", sync);
    control.addEventListener("change", sync);
  }
  sync();
}

// rememberRun keeps the exact inputs of a run this page started, so Replay can
// give them back later. The run log only holds a guess of them.
function rememberRun(id, run) {
  if (!id) return;
  try {
    localStorage.setItem(`dots-run-${id}`, JSON.stringify(run));
  } catch {
    // Private mode or a full quota: replay still works from the log.
  }
}

export function recalledRun(id) {
  if (!id) return null;
  try {
    return JSON.parse(localStorage.getItem(`dots-run-${id}`) || "null");
  } catch {
    return null;
  }
}
