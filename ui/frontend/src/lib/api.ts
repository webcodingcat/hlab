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

/** runApi runs one request that answers with action and/or next_actions under an action toast. */
export function runApi<T extends ActionResult>(
  project: string,
  title: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<T | undefined> {
  return runAction(title, () => api<T>(project, method, path, body), {
    get: async (id) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action,
  });
}

/** runOn runs a request on the named resource under the toast "<Type> <name>: <action>", the type taken from its path. */
export function runOn<T extends ActionResult>(
  project: string,
  name: string,
  action: string,
  req: { method: string; path: string; body?: unknown },
): Promise<T | undefined> {
  return runApi<T>(project, actionTitle(resourceType(req.path), name, action), req.method, req.path, req.body);
}
