// Network actions: the create form's request, the detail page's actions menu
// and the actions of the detail tabs (subnets, routes) and the list row menu.
import { changeProtection, createAt, pick, postAction, remove, type ResourceAction } from "../resourceActions";

const base = "networks";

/** create sends name, IP range and labels. */
export const create: ResourceAction = createAt(base, "Create network", (i) => pick(i, ["name", "ip_range", "labels"]));

// Change IP range only grows the range; HLab refuses it while servers are attached.
const changeIpRange = postAction(base, "change_ip_range", "change_ip_range", {
  label: "Change IP range",
  body: (i) => ({ ip_range: i.ip_range }),
  confirm: "plain",
  needsInput: true,
});

const protection = changeProtection(base);

const del = remove(base, "Delete", "typed-name");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [changeIpRange, protection, del];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  // HLab knows only cloud subnets (vswitch is answered with 422), so the
  // type is fixed and expose_routes_to_vswitch never sent.
  add_subnet: postAction(base, "add_subnet", "add_subnet", {
    label: "Add subnet",
    body: (i) => ({ type: "cloud", ...pick(i, ["network_zone", "ip_range"]) }),
    needsInput: true,
  }),
  delete_subnet: postAction(base, "delete_subnet", "delete_subnet", {
    label: "Delete subnet",
    body: (i) => ({ ip_range: i.ip_range }),
    confirm: "plain",
  }),
  add_route: postAction(base, "add_route", "add_route", {
    label: "Add route",
    body: (i) => pick(i, ["destination", "gateway"]),
    needsInput: true,
  }),
  delete_route: postAction(base, "delete_route", "delete_route", {
    label: "Delete route",
    body: (i) => pick(i, ["destination", "gateway"]),
    confirm: "plain",
  }),
  delete: del,
};
