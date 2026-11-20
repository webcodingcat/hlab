// Placement group actions: the create form's request, the detail page's
// actions menu and the actions of the list row menu.
import { createAt, pick, remove, update, type ResourceAction } from "../resourceActions";

const base = "placement_groups";

/** create sends name, labels and the type spread, the only type there is. */
export const create: ResourceAction = createAt(base, "Create placement group", (i) => ({
  ...pick(i, ["name", "labels"]),
  type: "spread",
}));

const rename = update(base, "rename", "Rename", (i) => ({ name: i.name }), { needsInput: true });

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [rename, del];

/** tabs are the actions of the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  delete: del,
};
