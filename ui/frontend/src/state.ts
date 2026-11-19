// Shell state: the cloud state from the tray's `cloud` event, the active
// view in location.hash and the project switcher.
import { derived, get, writable } from "svelte/store";
import { mgmt, on, service } from "./lib/bridge";
import { showError } from "./lib/toast";

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

// Views switch by location.hash ("#/projects"); the tray's asset server has
// no history routing.
export type ViewId = "projects" | "server-types" | "settings" | "logs";

export const views: { id: ViewId; title: string }[] = [
  { id: "projects", title: "Projects" },
  { id: "server-types", title: "Server Types" },
  { id: "settings", title: "Settings" },
  { id: "logs", title: "Logs" },
];

function viewFromHash(): ViewId {
  const id = location.hash.replace(/^#\/?/, "").split("/")[0];
  return views.find((v) => v.id === id)?.id ?? "projects";
}

export const view = writable<ViewId>(viewFromHash());

/** initView follows location.hash. */
export function initView(): () => void {
  const update = () => view.set(viewFromHash());
  window.addEventListener("hashchange", update);
  return () => window.removeEventListener("hashchange", update);
}

/** Project as GET /projects lists it. */
export type Project = { name: string; created: string; resources: number };

export const projects = writable<Project[]>([]);

const projectKey = "hlab.project";

/** project is the active project of the switcher, kept in localStorage. */
export const project = writable<string>(localStorage.getItem(projectKey) ?? "");
project.subscribe((p) => {
  if (p) localStorage.setItem(projectKey, p);
});

/** loadProjects reads the project list; the switcher keeps its project while it exists. */
export async function loadProjects(): Promise<void> {
  try {
    const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
    const list = res.projects ?? [];
    projects.set(list);
    if (list.length > 0 && !list.some((p) => p.name === get(project))) project.set(list[0].name);
  } catch (err) {
    showError("Load projects", err);
  }
}
