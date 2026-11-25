// Firewall actions: the create form's request, the detail page's actions menu
// and the actions of the detail tabs (rules, resources) and the list row menu.
// Firewalls apply to servers and to label selectors, in the create form and
// in the resources tab.
import { createAt, pick, postAction, remove, rename, type Input, type ResourceAction } from "../resourceActions";

const base = "firewalls";

/** server is the resource of input.server, the server's ID. */
const server = (i: Input) => ({ type: "server", server: { id: Number(i.server) } });

/** labelSelector is the resource of input.label_selector, the selector expression. */
const labelSelector = (i: Input) => ({ type: "label_selector", label_selector: { selector: i.label_selector } });

/** create sends name, labels, rules and the resources to apply the firewall to. */
export const create: ResourceAction = createAt(base, "Create firewall", (i) =>
  pick(i, ["name", "labels", "rules", "apply_to"]),
);

const del = remove(base, "Delete", "typed-name");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [rename(base), del];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  set_rules: postAction(base, "set_rules", "set_rules", {
    label: "Save rules",
    body: (i) => ({ rules: i.rules ?? [] }),
  }),
  apply_to_server: postAction(base, "apply_to_server", "apply_to_resources", {
    label: "Apply to server",
    body: (i) => ({ apply_to: [server(i)] }),
    needsInput: true,
  }),
  apply_to_label_selector: postAction(base, "apply_to_label_selector", "apply_to_resources", {
    label: "Apply to label selector",
    body: (i) => ({ apply_to: [labelSelector(i)] }),
    needsInput: true,
  }),
  remove_from_server: postAction(base, "remove_from_server", "remove_from_resources", {
    label: "Remove server",
    body: (i) => ({ remove_from: [server(i)] }),
    confirm: "plain",
  }),
  remove_from_label_selector: postAction(base, "remove_from_label_selector", "remove_from_resources", {
    label: "Remove label selector",
    body: (i) => ({ remove_from: [labelSelector(i)] }),
    confirm: "plain",
  }),
  delete: del,
};
