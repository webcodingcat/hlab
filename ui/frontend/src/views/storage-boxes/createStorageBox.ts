// Creating a storage box: the request under an action toast that follows the
// create action to its end. The project is a plain value taken when the
// form is sent: the create action runs for minutes (until the storage VM has
// started), long after the form has closed, and a component prop read then
// follows the current route instead (undefined outside a project console).
import { runAction, type Action, type ActionResult } from "../../lib/actions";
import { api, hetznerApi } from "../../lib/api";
import { actionTitle, resourceType } from "../../lib/format";
import { request, type Input } from "../../lib/resourceActions";
import { create } from "../../lib/resources/storageBoxes";

/** Hooks lets the form react to the answer: onanswer once the box exists, onerror when the request fails. */
export type Hooks = { onanswer?: () => void; onerror?: (err: unknown) => void };

/** createStorageBox sends the create request of project and follows its action; undefined after an error. */
export function createStorageBox(project: string, input: Input, hooks: Hooks = {}): Promise<ActionResult | undefined> {
  const req = request(create, 0, input)!;
  return runAction(
    actionTitle(resourceType(req.path), String(input.name ?? ""), "Create"),
    async () => {
      try {
        const res = await hetznerApi<ActionResult>(project, req.method, req.path, req.body);
        hooks.onanswer?.();
        return res;
      } catch (err) {
        hooks.onerror?.(err);
        throw err;
      }
    },
    { get: async (id) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action },
  );
}
