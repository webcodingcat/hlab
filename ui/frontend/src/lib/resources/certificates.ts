// Certificate actions: the create form's request, the detail page's actions
// menu and the actions of the list row menu.
import { createAt, pick, postAction, remove, rename, type ResourceAction } from "../resourceActions";

const base = "certificates";

/**
 * create sends type, name and labels; an uploaded certificate also its
 * certificate and private key, a managed one its domain names.
 */
export const create: ResourceAction = createAt(base, "Add certificate", (i) =>
  i.type === "managed"
    ? pick(i, ["type", "name", "domain_names", "labels"])
    : pick(i, ["type", "name", "certificate", "private_key", "labels"]),
);

// Retry issues a failed managed certificate again; the detail page shows it
// only then (HLab answers 422 otherwise).
const retry = postAction(base, "retry", "retry", { label: "Retry" });

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [rename(base), retry, del];

/** tabs are the actions of the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  delete: del,
};
