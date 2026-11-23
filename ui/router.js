// Hash routing. The URL is the only state a reload must preserve:
//   #activity
//   #workflows[/<name>]
//   #config[/<file>]
//   #repos
//   #costs
//   #memory

const VIEWS = new Set(["activity", "workflows", "config", "repos", "costs", "memory"]);

export function parseRoute(hash = location.hash) {
  const [view = "activity", ...rest] = hash.replace(/^#\/?/, "").split("/");
  return {
    view: VIEWS.has(view) ? view : "activity",
    arg: rest.join("/") || "",
  };
}

export function routeHref(view, arg = "") {
  return `#${view}${arg ? `/${arg}` : ""}`;
}

export function navigate(view, arg = "", { replace = false } = {}) {
  const href = routeHref(view, arg);
  if (location.hash === href) return;
  if (replace) location.replace(href);
  else location.hash = href;
}

export function startRouter(onRoute) {
  let current = null;
  const run = () => {
    const route = parseRoute();
    const key = `${route.view}/${route.arg}`;
    if (key === current) return;
    current = key;
    onRoute(route);
  };
  window.addEventListener("hashchange", run);
  if (!location.hash) history.replaceState(null, "", "#activity");
  run();
  return () => window.removeEventListener("hashchange", run);
}
