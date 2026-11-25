// Creating a storage box: the request under an action toast that follows the
// create action to its end. The project is a plain value taken when the
// form is sent: the create action runs for minutes (until the storage VM has
// started), long after the form has closed, and a component prop read then
// follows the current route instead (undefined outside a project console).
import type { ActionResult } from "../../lib/actions";
import { runHetznerOn, type Hooks } from "../../lib/api";
import { request, type Input } from "../../lib/resourceActions";
import { create } from "../../lib/resources/storageBoxes";

/** createStorageBox sends the create request of project and follows its action; undefined after an error. */
export function createStorageBox(
  project: string,
  input: Input,
  hooks: Hooks<ActionResult> = {},
): Promise<ActionResult | undefined> {
  return runHetznerOn(project, String(input.name ?? ""), "Create", request(create, 0, input)!, hooks);
}
