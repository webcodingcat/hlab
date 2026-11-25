// The Hetzner API of one project through the management API's project
// proxy (/mgmt/v1/projects/{name}/api/v1...).
import { mgmt } from "./bridge";
import { runAction, type Action, type ActionResult } from "./actions";
import { actionTitle, resourceType } from "./format";

const perPage = 50;

/** api sends one request to the project's Hetzner API; an error status throws ApiError. */
export function api<T>(project: string, method: string, path: string, body?: unknown): Promise<T> {
  return mgmt<T>(method, `/projects/${encodeURIComponent(project)}/api/v1${path}`, body);
}

type Page = { meta?: { pagination?: { next_page?: number | null } } };

/** listAll reads every page of a list endpoint and returns the items under key. */
export async function listAll<T>(project: string, path: string, key: string): Promise<T[]> {
  const sep = path.includes("?") ? "&" : "?";
  const items: T[] = [];
  let page: number | null | undefined = 1;
  while (page) {
    const res: Page & Record<string, unknown> = await api(project, "GET", `${path}${sep}page=${page}&per_page=${perPage}`);
    items.push(...((res[key] as T[] | undefined) ?? []));
    page = res.meta?.pagination?.next_page;
  }
  return items;
}

/**
 * Hooks lets a form react to the answer of its request: onanswer once the
 * API answered, onerror when the request failed. The actions are followed
 * after onanswer in the project passed as a plain value, so a form that
 * closes or a route that changes meanwhile does not move them elsewhere.
 */
export type Hooks<T> = { onanswer?: (res: T) => void; onerror?: (err: unknown) => void };

function answered<T>(send: () => Promise<T>, hooks: Hooks<T>): () => Promise<T> {
  return async () => {
    let res: T;
    try {
      res = await send();
    } catch (err) {
      hooks.onerror?.(err);
      throw err;
    }
    hooks.onanswer?.(res);
    return res;
  };
}

function poll(project: string) {
  return { get: async (id: number) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action };
}

/** runApi runs one request that answers with action and/or next_actions under an action toast. */
export function runApi<T extends ActionResult>(
  project: string,
  title: string,
  method: string,
  path: string,
  body?: unknown,
  hooks: Hooks<T> = {},
): Promise<T | undefined> {
  return runAction(title, answered(() => api<T>(project, method, path, body), hooks), poll(project));
}

/** runOn runs a request on the named resource under the toast "<Type> <name>: <action>", the type taken from its path. */
export function runOn<T extends ActionResult>(
  project: string,
  name: string,
  action: string,
  req: { method: string; path: string; body?: unknown },
  hooks: Hooks<T> = {},
): Promise<T | undefined> {
  return runApi<T>(project, actionTitle(resourceType(req.path), name, action), req.method, req.path, req.body, hooks);
}

/** hetznerApi sends one request to the project's Hetzner API (/mgmt/v1/projects/{name}/api/hetzner/v1<path>). */
export function hetznerApi<T>(project: string, method: string, path: string, body?: unknown): Promise<T> {
  return mgmt<T>(method, `/projects/${encodeURIComponent(project)}/api/hetzner/v1${path}`, body);
}

/** hetznerListAll reads every page of a Hetzner API list endpoint; a response without meta is one page. */
export async function hetznerListAll<T>(project: string, path: string, key: string): Promise<T[]> {
  const sep = path.includes("?") ? "&" : "?";
  const items: T[] = [];
  let page: number | null | undefined = 1;
  while (page) {
    const res: Page & Record<string, unknown> = await hetznerApi(
      project,
      "GET",
      `${path}${sep}page=${page}&per_page=${perPage}`,
    );
    items.push(...((res[key] as T[] | undefined) ?? []));
    page = res.meta?.pagination?.next_page;
  }
  return items;
}

/**
 * runHetzner runs one Hetzner API request under an action toast; the action
 * is polled through the Cloud API's GET /actions/{id}, which also returns
 * storage box actions.
 */
export function runHetzner<T extends ActionResult>(
  project: string,
  title: string,
  method: string,
  path: string,
  body?: unknown,
  hooks: Hooks<T> = {},
): Promise<T | undefined> {
  return runAction(title, answered(() => hetznerApi<T>(project, method, path, body), hooks), poll(project));
}

/** runHetznerOn runs a Hetzner API request on the named resource under the toast "<Type> <name>: <action>". */
export function runHetznerOn<T extends ActionResult>(
  project: string,
  name: string,
  action: string,
  req: { method: string; path: string; body?: unknown },
  hooks: Hooks<T> = {},
): Promise<T | undefined> {
  return runHetzner<T>(
    project,
    actionTitle(resourceType(req.path), name, action),
    req.method,
    req.path,
    req.body,
    hooks,
  );
}

type Named = { id: number; name: string };

/**
 * faultTargetNames reads the names of a project's servers, volumes and
 * storage boxes keyed "server:42", "volume:7", "storage_box:3" for the
 * target column of the faults tables. Best-effort: a list that fails (an
 * api_error fault on reads, for example) leaves its names out.
 */
export async function faultTargetNames(project: string): Promise<[string, string][]> {
  const lists = await Promise.allSettled([
    listAll<Named>(project, "/servers", "servers"),
    listAll<Named>(project, "/volumes", "volumes"),
    hetznerListAll<Named>(project, "/storage_boxes", "storage_boxes"),
  ]);
  const prefixes = ["server", "volume", "storage_box"];
  return lists.flatMap((r, i) =>
    r.status === "fulfilled" ? r.value.map((x) => [`${prefixes[i]}:${x.id}`, x.name] as [string, string]) : [],
  );
}
