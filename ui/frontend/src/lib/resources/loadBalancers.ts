// Load balancer actions: the create form's request, the detail page's actions
// menu and the actions of the detail tabs (services, targets, networking).
import {
  changeDnsPtr,
  changeProtection,
  createAt,
  pick,
  postAction,
  remove,
  type Input,
  type ResourceAction,
} from "../resourceActions";

const base = "load_balancers";

/**
 * service builds a service from the form input: tcp sends no http block;
 * http and https send sticky sessions and cookie, https also certificates
 * and redirect_http.
 */
function service(i: Input): Input {
  const out = pick(i, ["protocol", "listen_port", "destination_port", "proxyprotocol", "health_check"]);
  const http = i.http as Input | undefined;
  if (http !== undefined && (i.protocol === "http" || i.protocol === "https")) {
    const keys = ["sticky_sessions", "cookie_name", "cookie_lifetime"];
    if (i.protocol === "https") keys.push("certificates", "redirect_http");
    out.http = pick(http, keys);
  }
  return out;
}

/** target builds a server, label selector or IP target; use_private_ip only for the first two. */
function target(t: Input): Input {
  const keys = ["type", String(t.type)];
  if (t.type !== "ip") keys.push("use_private_ip");
  return pick(t, keys);
}

/** create sends the form's fields with its targets and services built as for the tabs. */
export const create: ResourceAction = createAt(base, "Create load balancer", (i) => {
  const out = pick(i, ["name", "load_balancer_type", "location", "network", "algorithm", "public_interface", "labels"]);
  if (Array.isArray(i.targets)) out.targets = (i.targets as Input[]).map(target);
  if (Array.isArray(i.services)) out.services = (i.services as Input[]).map(service);
  return out;
});

const changeType = postAction(base, "change_type", "change_type", {
  label: "Change type",
  body: (i) => ({ load_balancer_type: i.load_balancer_type }),
  confirm: "plain",
  needsInput: true,
});

const changeAlgorithm = postAction(base, "change_algorithm", "change_algorithm", {
  label: "Change algorithm",
  body: (i) => ({ type: i.type }),
  needsInput: true,
});

const enablePublic = postAction(base, "enable_public_interface", "enable_public_interface", {
  label: "Enable public interface",
});

const disablePublic = postAction(base, "disable_public_interface", "disable_public_interface", {
  label: "Disable public interface",
  confirm: "plain",
});

const dnsPtr = changeDnsPtr(base);

const protection = changeProtection(base);

const del = remove(base, "Delete", "plain");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [
  changeType,
  changeAlgorithm,
  enablePublic,
  disablePublic,
  dnsPtr,
  protection,
  del,
];

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  add_service: postAction(base, "add_service", "add_service", {
    label: "Add service",
    body: service,
    needsInput: true,
  }),
  update_service: postAction(base, "update_service", "update_service", {
    label: "Update service",
    body: service,
    needsInput: true,
  }),
  delete_service: postAction(base, "delete_service", "delete_service", {
    label: "Delete service",
    body: (i) => ({ listen_port: i.listen_port }),
    confirm: "plain",
  }),
  add_target: postAction(base, "add_target", "add_target", {
    label: "Add target",
    body: target,
    needsInput: true,
  }),
  remove_target: postAction(base, "remove_target", "remove_target", {
    label: "Remove target",
    body: (i) => pick(i, ["type", String(i.type)]),
    confirm: "plain",
  }),
  attach_to_network: postAction(base, "attach_to_network", "attach_to_network", {
    label: "Attach to network",
    body: (i) => pick(i, ["network", "ip"]),
    needsInput: true,
  }),
  detach_from_network: postAction(base, "detach_from_network", "detach_from_network", {
    label: "Detach from network",
    body: (i) => ({ network: i.network }),
    confirm: "plain",
  }),
  delete: del,
};
