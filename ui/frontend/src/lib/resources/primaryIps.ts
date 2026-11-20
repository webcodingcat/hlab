// Primary IP actions: the create form's request, the detail page's actions
// menu and the actions of the detail tabs and list row menus.
import {
  changeDnsPtr,
  changeProtection,
  createAt,
  pick,
  postAction,
  remove,
  update,
  type ResourceAction,
} from "../resourceActions";

const base = "primary_ips";

/**
 * create sends name, type, auto_delete and labels; with a server to assign
 * it to the server as assignee (its location applies), without one the
 * location. A primary IP has no datacenter.
 */
export const create: ResourceAction = createAt(base, "Create primary IP", (i) =>
  i.assignee_id !== undefined
    ? { ...pick(i, ["name", "type"]), assignee_type: "server", ...pick(i, ["assignee_id", "auto_delete", "labels"]) }
    : pick(i, ["name", "type", "location", "auto_delete", "labels"]),
);

const assign = postAction(base, "assign", "assign", {
  label: "Assign",
  body: (i) => ({ assignee_type: "server", assignee_id: i.assignee_id }),
  needsInput: true,
});

const unassign = postAction(base, "unassign", "unassign", { label: "Unassign", confirm: "plain" });

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [assign, unassign, changeDnsPtr(base), changeProtection(base), del];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  auto_delete: update(base, "auto_delete", "Change auto delete", (i) => ({ auto_delete: i.auto_delete })),
  unassign,
  delete: del,
};
