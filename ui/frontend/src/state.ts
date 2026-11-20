// Shell state: the cloud state from the tray's `cloud` event, the route in
// location.hash and the last opened project.
import { derived, get, writable, type Readable } from "svelte/store";
import { mgmt, on, service } from "./lib/bridge";
import { showError } from "./lib/toast";
import type { ViewId } from "./help";
import viewIds from "./views.json";

export type { ViewId };

/** Problem is one problem of hlabd (status file or /status). */
export type Problem = { code: string; message: string; fatal: boolean };

/** CloudState mirrors bridge.State. */
export type CloudState = {
  state: "" | "stopped" | "starting" | "running" | "stopping" | "refused";
  problems: Problem[];
  refused?: Problem;
  error?: string;
  connection_lost: boolean;
  version?: string;
};

export const cloud = writable<CloudState>({ state: "", problems: [], connection_lost: false });

export const running = derived(cloud, ($c) => $c.state === "running");

/** initCloud reads the current state and follows the `cloud` event. */
export function initCloud(): () => void {
  const off = on<CloudState>("cloud", (s) => cloud.set(s));
  service<CloudState>("Cloud", "Status").then(
    (s) => cloud.set(s),
    (err) => showError("Read the cloud state", err),
  );
  return off;
}

/** startCloud starts the cloud; discard asks hlabd to discard its state. */
export function startCloud(discard = false): Promise<void> {
  return service<void>("Cloud", "Start", discard);
}

export function stopCloud(): Promise<void> {
  return service<void>("Cloud", "Stop");
}

// The shell has three areas: the project list, the console of one project
// and the Administration. Routes live in location.hash; the tray's asset
// server has no history routing.

type Area = "projects" | "project" | "admin";
type AdminView = "server-types" | "settings" | "logs";

export type Route =
  | { area: "projects" }
  | { area: "project"; project: string; view: "tokens" }
  | { area: "admin"; view: AdminView };

const viewInfo: Record<ViewId, { title: string; area: Area }> = {
  projects: { title: "Projects", area: "projects" },
  tokens: { title: "API tokens", area: "project" },
  "server-types": { title: "Server Types", area: "admin" },
  settings: { title: "Settings", area: "admin" },
  logs: { title: "Logs", area: "admin" },
};

/** views are the views of views.json in its order, with title and area. */
export const views: { id: ViewId; title: string; area: Area }[] = (viewIds as ViewId[]).map((id) => ({
  id,
  ...viewInfo[id],
}));

/** href is the hash of a route. */
export function href(r: Route): string {
  switch (r.area) {
    case "projects":
      return "#/projects";
    case "project":
      return `#/projects/${encodeURIComponent(r.project)}/${r.view}`;
    case "admin":
      return `#/admin/${r.view}`;
  }
}

/** parse reads a hash; undefined for an unknown route. */
function parse(hash: string): Route | undefined {
  const parts = hash.replace(/^#\/?/, "").split("/");
  const known = (id: string, area: Area) => views.some((v) => v.id === id && v.area === area);
  if (parts.length === 1 && parts[0] === "projects") return { area: "projects" };
  if (parts.length === 3 && parts[0] === "projects" && parts[1] && known(parts[2], "project")) {
    return { area: "project", project: decodeURIComponent(parts[1]), view: "tokens" };
  }
  if (parts.length === 2 && parts[0] === "admin" && known(parts[1], "admin")) {
    return { area: "admin", view: parts[1] as AdminView };
  }
  return undefined;
}

const current = writable<Route>(parse(location.hash) ?? { area: "projects" });

/** route is the route of location.hash. */
export const route: Readable<Route> = { subscribe: current.subscribe };

const outsideAdmin = writable<string>(href({ area: "projects" }));

/**
 * adminReturn is the hash of the last route shown outside Administration,
 * the project list when there was none; the gear returns there.
 */
export const adminReturn: Readable<string> = { subscribe: outsideAdmin.subscribe };

const projectKey = "hlab.project";

/** lastProject is the last opened project, kept in localStorage. */
export const lastProject = writable<string>(localStorage.getItem(projectKey) ?? "");
lastProject.subscribe((p) => {
  if (p) localStorage.setItem(projectKey, p);
});

/** Project as GET /projects lists it. */
export type Project = { name: string; created: string; resources: number };

/** startHash is the console of the last opened project while it exists, else the project list. */
async function startHash(): Promise<string> {
  const name = get(lastProject);
  if (name) {
    try {
      const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
      if ((res.projects ?? []).some((p) => p.name === name)) return href({ area: "project", project: name, view: "tokens" });
    } catch {
      // The cloud does not run: the project list says how to start it.
    }
  }
  return href({ area: "projects" });
}

const emptyHash = () => location.hash === "" || location.hash === "#";

/**
 * initRoute follows location.hash. An empty hash opens the start route,
 * an unknown route falls back to the project list.
 */
export function initRoute(): () => void {
  const update = () => {
    if (emptyHash()) return;
    const r = parse(location.hash);
    if (!r) {
      location.replace(href({ area: "projects" }));
      return;
    }
    if (r.area === "project") lastProject.set(r.project);
    if (r.area !== "admin") outsideAdmin.set(href(r));
    current.set(r);
  };
  window.addEventListener("hashchange", update);
  if (emptyHash()) {
    startHash().then((h) => {
      if (emptyHash()) location.replace(h);
    });
  } else {
    update();
  }
  return () => window.removeEventListener("hashchange", update);
}
