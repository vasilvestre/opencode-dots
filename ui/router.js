// Hash routing. The URL is the only state a reload must preserve:
//   #activity
//   #workflows[/<name>]
//   #config[/<file>]
//   #repos
//   #costs
//   #memory
//   #autonomy
//   #health
//   #audit
//   #links

const VIEWS = new Set(["activity", "workflows", "config", "repos", "costs", "memory", "autonomy", "health", "audit", "links"]);

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

let leaveGuard = null;

// A view with unsaved work sets a guard; it runs before any route change and
// answers false to stay where it is.
export function setLeaveGuard(fn) {
  leaveGuard = fn;
}

export function startRouter(onRoute) {
  let current = null;
  let currentRoute = null;
  const run = async () => {
    const route = parseRoute();
    const key = `${route.view}/${route.arg}`;
    if (key === current) return;
    if (currentRoute && leaveGuard && !(await leaveGuard(route))) {
      history.replaceState(null, "", routeHref(currentRoute.view, currentRoute.arg));
      return;
    }
    current = key;
    currentRoute = route;
    onRoute(route);
  };
  window.addEventListener("hashchange", run);
  if (!location.hash) history.replaceState(null, "", "#activity");
  run();
  return () => window.removeEventListener("hashchange", run);
}
