// Volume actions: the create form's request, the detail page's actions menu
// and the actions of the detail tabs and list row menus.
import {
  changeProtection,
  createAt,
  pick,
  postAction,
  remove,
  type ResourceAction,
} from "../resourceActions";

const base = "volumes";

/**
 * create sends size, name and labels; without a server the location, with
 * one the server, automount and format instead (the server's location applies).
 */
export const create: ResourceAction = createAt(base, "Create volume", (i) =>
  i.server !== undefined
    ? pick(i, ["name", "size", "server", "automount", "format", "labels"])
    : pick(i, ["name", "size", "location", "labels"]),
);

const resize = postAction(base, "resize", "resize", {
  label: "Resize",
  body: (i) => ({ size: i.size }),
  needsInput: true,
});

const attach = postAction(base, "attach", "attach", {
  label: "Attach",
  body: (i) => ({ assignee: { id: i.server, type: "server" }, automount: i.automount === true }),
  needsInput: true,
});

const detach = postAction(base, "detach", "detach", { label: "Detach", confirm: "plain" });

const protection = changeProtection(base);

const del = remove(base, "Delete", "typed-name");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [resize, attach, detach, protection, del];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  resize,
  attach,
  detach,
  delete: del,
};
