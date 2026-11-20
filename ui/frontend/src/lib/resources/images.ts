// Image actions: the detail page's actions menu and the actions of the list
// row menu. Images have no create form; a server's Backups and Snapshots
// tabs make them.
import { changeProtection, remove, update, type ResourceAction } from "../resourceActions";

const base = "images";

const description = update(base, "description", "Edit description", (i) => ({ description: i.description }), {
  needsInput: true,
});

// Convert to snapshot turns a backup into a snapshot bound to no server; the
// detail page shows it only for backups.
const convert = update(base, "convert", "Convert to snapshot", () => ({ type: "snapshot" }));

const protection = changeProtection(base);

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [description, convert, protection, del];

/** tabs are the actions of the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  delete: del,
};
