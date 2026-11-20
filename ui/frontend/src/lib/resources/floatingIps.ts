// Floating IP actions: the create form's request, the detail page's actions
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

const base = "floating_ips";

/**
 * create sends type, name, description and labels; with a server the
 * server (its location becomes the home location), without one the home
 * location.
 */
export const create: ResourceAction = createAt(base, "Create floating IP", (i) =>
  i.server !== undefined
    ? pick(i, ["type", "server", "name", "description", "labels"])
    : pick(i, ["type", "home_location", "name", "description", "labels"]),
);

const assign = postAction(base, "assign", "assign", {
  label: "Assign",
  body: (i) => ({ server: i.server }),
  needsInput: true,
});

const unassign = postAction(base, "unassign", "unassign", { label: "Unassign", confirm: "plain" });

const dnsPtr = changeDnsPtr(base);

const protection = changeProtection(base);

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [assign, unassign, dnsPtr, protection, del];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  description: update(base, "description", "Change description", (i) => ({ description: i.description }), {
    needsInput: true,
  }),
  unassign,
  delete: del,
};
