// Routes of the window: the hash of each page and back. Pure, so the unit
// tests load it without a browser; state.ts keeps the stores.

export type ProjectView =
  | "servers"
  | "volumes"
  | "storage-boxes"
  | "load-balancers"
  | "networks"
  | "firewalls"
  | "floating-ips"
  | "primary-ips"
  | "dns"
  | "certificates"
  | "ssh-keys"
  | "placement-groups"
  | "snapshots"
  | "backups"
  | "tokens";
export type AdminView = "server-types" | "storage-box-types" | "isos" | "settings" | "logs";
export type Page = { kind: "list" } | { kind: "new" } | { kind: "detail"; id: number; tab: string };
export type Route =
  | { area: "projects" }
  | { area: "project"; project: string; view: ProjectView; page: Page }
  | { area: "admin"; view: AdminView }
  // The console window of one server, opened by the tray outside the shell.
  | { area: "server-console"; project: string; server: number };

const projectViews: ProjectView[] = [
  "servers",
  "volumes",
  "storage-boxes",
  "load-balancers",
  "networks",
  "firewalls",
  "floating-ips",
  "primary-ips",
  "dns",
  "certificates",
  "ssh-keys",
  "placement-groups",
  "snapshots",
  "backups",
  "tokens",
];
const adminViews: AdminView[] = ["server-types", "storage-box-types", "isos", "settings", "logs"];
// Views with only a list page.
const listOnly: ProjectView[] = ["tokens"];
// Views with a list and a detail page but no create page.
const noNew: ProjectView[] = ["snapshots", "backups"];

/** href is the hash of a route. */
export function href(r: Route): string {
  switch (r.area) {
    case "projects":
      return "#/projects";
    case "admin":
      return `#/admin/${r.view}`;
    case "server-console":
      return `#/server-console/${encodeURIComponent(r.project)}/${r.server}`;
    case "project": {
      const base = `#/projects/${encodeURIComponent(r.project)}/${r.view}`;
      switch (r.page.kind) {
        case "list":
          return base;
        case "new":
          return `${base}/new`;
        case "detail":
          return `${base}/${r.page.id}/${r.page.tab}`;
      }
    }
  }
}

function decode(s: string): string | undefined {
  try {
    return decodeURIComponent(s);
  } catch {
    return undefined;
  }
}

/**
 * parse reads a hash; undefined for an unknown route. A detail without tab
 * or with a tab not of the form [a-z-]+ gets tab "overview".
 */
export function parse(hash: string): Route | undefined {
  const parts = hash.replace(/^#\/?/, "").split("/");
  if (parts.length === 1 && parts[0] === "projects") return { area: "projects" };
  if (parts.length === 2 && parts[0] === "admin" && adminViews.includes(parts[1] as AdminView)) {
    return { area: "admin", view: parts[1] as AdminView };
  }
  if (parts.length === 3 && parts[0] === "server-console") {
    const project = decode(parts[1]);
    if (!project || !/^[1-9][0-9]*$/.test(parts[2])) return undefined;
    return { area: "server-console", project, server: Number(parts[2]) };
  }
  if (parts[0] !== "projects" || parts.length < 3 || parts.length > 5) return undefined;
  const project = decode(parts[1]);
  const view = parts[2] as ProjectView;
  if (!project || !projectViews.includes(view)) return undefined;
  const route = (page: Page): Route => ({ area: "project", project, view, page });
  if (parts.length === 3) return route({ kind: "list" });
  if (listOnly.includes(view)) return undefined;
  if (parts.length === 4 && parts[3] === "new") return noNew.includes(view) ? undefined : route({ kind: "new" });
  if (!/^[1-9][0-9]*$/.test(parts[3])) return undefined;
  const tab = parts.length === 5 && /^[a-z-]+$/.test(parts[4]) ? parts[4] : "overview";
  return route({ kind: "detail", id: Number(parts[3]), tab });
}

/** consoleStart is the first page of a project console: its Servers list. */
export function consoleStart(project: string): Route {
  return { area: "project", project, view: "servers", page: { kind: "list" } };
}
