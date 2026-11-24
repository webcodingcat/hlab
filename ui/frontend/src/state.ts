// Shell state: the cloud state from the tray's `cloud` event, the route in
// location.hash and the last opened project.
import { derived, get, writable, type Readable } from "svelte/store";
import { mgmt, on, service } from "./lib/bridge";
import { showError } from "./lib/toast";
import type { ViewId } from "./help";
import viewIds from "./views.json";
import { consoleStart, href, parse, type AdminView, type Page, type ProjectView, type Route } from "./lib/routes";
import type { Layout, Project, ProjectSort } from "./lib/projects";

export type { AdminView, Page, Project, ProjectView, Route, ViewId };
export { consoleStart, href };

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
  faults: number; // active faults of all projects; 0 unless running
};

export const cloud = writable<CloudState>({ state: "", problems: [], connection_lost: false, faults: 0 });

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

type Area = Route["area"];

const viewInfo: Record<ViewId, { title: string; area: Area }> = {
  projects: { title: "Projects", area: "projects" },
  servers: { title: "Servers", area: "project" },
  volumes: { title: "Volumes", area: "project" },
  "storage-boxes": { title: "Storage Boxes", area: "project" },
  "load-balancers": { title: "Load Balancers", area: "project" },
  networks: { title: "Networks", area: "project" },
  firewalls: { title: "Firewalls", area: "project" },
  "floating-ips": { title: "Floating IPs", area: "project" },
  "primary-ips": { title: "Primary IPs", area: "project" },
  dns: { title: "DNS", area: "project" },
  certificates: { title: "Certificates", area: "project" },
  "ssh-keys": { title: "SSH Keys", area: "project" },
  "placement-groups": { title: "Placement Groups", area: "project" },
  snapshots: { title: "Snapshots", area: "project" },
  backups: { title: "Backups", area: "project" },
  isos: { title: "ISOs", area: "admin" },
  tokens: { title: "API tokens", area: "project" },
  "server-types": { title: "Server Types", area: "admin" },
  "storage-box-types": { title: "Storage Box Types", area: "admin" },
  settings: { title: "Settings", area: "admin" },
  logs: { title: "Logs", area: "admin" },
  faults: { title: "Faults", area: "project" },
  "active-faults": { title: "Active faults", area: "faults" },
};

/** views are the views of views.json in its order, with title and area. */
export const views: { id: ViewId; title: string; area: Area }[] = (viewIds as ViewId[]).map((id) => ({
  id,
  ...viewInfo[id],
}));

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

const groupsKey = "hlab.collapsedGroups";

function storedGroups(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(groupsKey) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** collapsedGroups are the IDs of the collapsed sidebar groups, kept in localStorage. */
export const collapsedGroups = writable<string[]>(storedGroups());
collapsedGroups.subscribe((g) => localStorage.setItem(groupsKey, JSON.stringify(g)));

const sortKey = "hlab.projectSort";

function storedSort(): ProjectSort {
  try {
    const v = JSON.parse(localStorage.getItem(sortKey) ?? "null") as Partial<ProjectSort> | null;
    if (v && (v.key === "display_name" || v.key === "created") && (v.dir === "asc" || v.dir === "desc")) {
      return { key: v.key, dir: v.dir };
    }
  } catch {
    // An invalid stored value falls back to the default.
  }
  return { key: "created", dir: "asc" };
}

/** projectSort is the sort order of the project list, kept in localStorage. */
export const projectSort = writable<ProjectSort>(storedSort());
projectSort.subscribe((s) => localStorage.setItem(sortKey, JSON.stringify(s)));

const layoutKey = "hlab.projectLayout";

function storedLayout(): Layout {
  const v = localStorage.getItem(layoutKey);
  return v === "list" ? "list" : "grid";
}

/** projectLayout is the layout of the project list (grid or list), kept in localStorage. */
export const projectLayout = writable<Layout>(storedLayout());
projectLayout.subscribe((l) => localStorage.setItem(layoutKey, l));

/** startHash is the console of the last opened project while it exists, else the project list. */
async function startHash(): Promise<string> {
  const name = get(lastProject);
  if (name) {
    try {
      const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
      if ((res.projects ?? []).some((p) => p.name === name)) return href(consoleStart(name));
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
