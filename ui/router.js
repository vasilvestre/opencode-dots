// Hash routing. The URL is the only state a reload must preserve:
//   #home (the landing page)
//   #activity
//   #workflows[/<name>]
//   #config[/models|notifications|appearance]
//   #repos[/<file>]
//   #costs
//   #memory
//   #autonomy
//   #health
//   #audit
//   #links

const VIEWS = new Set(["home", "activity", "workflows", "config", "repos", "costs", "memory", "autonomy", "health", "audit", "links"]);

export function parseRoute(hash = location.hash) {
  const [view = "home", ...rest] = hash.replace(/^#\/?/, "").split("/");
  return {
    view: VIEWS.has(view) ? view : "home",
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
  if (!location.hash) history.replaceState(null, "", "#home");
  run();
  return () => window.removeEventListener("hashchange", run);
}
