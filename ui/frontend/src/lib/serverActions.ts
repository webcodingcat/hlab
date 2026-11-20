// Server actions: every entry of a server's actions menu and of its detail
// tabs, with the request it sends, its confirmation and whether the answer
// carries a one-time root password. Pure, so the unit tests load it without
// a browser; the Svelte views only render it.

export type Input = Record<string, unknown>;
export type Confirm = "none" | "plain" | "typed-name";
export type ServerAction = {
  id: string; // "poweron", "rename", "delete", "restore", "attach_volume", ...
  label: string; // menu or button label
  method?: "POST" | "PUT" | "DELETE";
  path?: (server: number, input: Input) => string;
  body?: (input: Input, server: number) => unknown;
  confirm: Confirm;
  password: boolean; // the answer carries root_password
  needsInput: boolean; // opens a dialog before it runs
  disabledHint?: string; // set: shown disabled, never sent
};

type Options = {
  label: string;
  body?: ServerAction["body"];
  confirm?: Confirm;
  password?: boolean;
  needsInput?: boolean;
};

/** serverAction is a POST to /servers/{id}/actions/<action>. */
function serverAction(id: string, action: string, o: Options): ServerAction {
  return {
    id,
    label: o.label,
    method: "POST",
    path: (server) => `/servers/${server}/actions/${action}`,
    body: o.body,
    confirm: o.confirm ?? "none",
    password: o.password ?? false,
    needsInput: o.needsInput ?? false,
  };
}

/** simple is a server action without input, named after its endpoint. */
function simple(action: string, label: string, confirm: Confirm = "none"): ServerAction {
  return serverAction(action, action, { label, confirm });
}

/** pick copies the named fields of input that are set. */
function pick(input: Input, keys: string[]): Input {
  const out: Input = {};
  for (const k of keys) {
    if (input[k] !== undefined) out[k] = input[k];
  }
  return out;
}

/** serverRef is the resource list of a firewall request for one server. */
const serverRef = (server: number) => [{ type: "server", server: { id: server } }];

export const menuActions: ServerAction[] = [
  simple("poweron", "Power on"),
  simple("shutdown", "Shutdown"),
  simple("reboot", "Reboot"),
  simple("reset", "Reset", "plain"),
  simple("poweroff", "Power off", "plain"),
  serverAction("reset_password", "reset_password", { label: "Reset root password", password: true }),
  serverAction("rebuild", "rebuild", {
    label: "Rebuild",
    body: (i) => ({ image: i.image }),
    confirm: "plain",
    password: true,
    needsInput: true,
  }),
  serverAction("change_type", "change_type", {
    label: "Change type",
    body: (i) => ({ server_type: i.server_type, upgrade_disk: i.upgrade_disk === true }),
    confirm: "plain",
    needsInput: true,
  }),
  {
    id: "rename",
    label: "Rename",
    method: "PUT",
    path: (server) => `/servers/${server}`,
    body: (i) => ({ name: i.name }),
    confirm: "none",
    password: false,
    needsInput: true,
  },
  serverAction("add_to_placement_group", "add_to_placement_group", {
    label: "Add to placement group",
    body: (i) => ({ placement_group: i.placement_group }),
    needsInput: true,
  }),
  simple("remove_from_placement_group", "Remove from placement group"),
  {
    id: "console",
    label: "Console",
    confirm: "none",
    password: false,
    needsInput: false,
    disabledHint: "Not available in HLab",
  },
  {
    id: "delete",
    label: "Delete",
    method: "DELETE",
    path: (server) => `/servers/${server}`,
    confirm: "typed-name",
    password: false,
    needsInput: false,
  },
];

const tabList: ServerAction[] = [
  simple("enable_backup", "Enable backups"),
  simple("disable_backup", "Disable backups"),
  serverAction("create_backup", "create_image", { label: "Create backup", body: () => ({ type: "backup" }) }),
  serverAction("create_snapshot", "create_image", {
    label: "Create snapshot",
    body: (i) => ({ type: "snapshot", ...pick(i, ["description"]) }),
    needsInput: true,
  }),
  serverAction("restore", "rebuild", {
    label: "Restore",
    body: (i) => ({ image: i.image }),
    confirm: "plain",
    password: true,
  }),
  {
    id: "delete_image",
    label: "Delete",
    method: "DELETE",
    path: (_server, i) => `/images/${i.image}`,
    confirm: "plain",
    password: false,
    needsInput: false,
  },
  serverAction("enable_rescue", "enable_rescue", {
    label: "Enable rescue",
    body: (i) => ({ type: "linux64", ssh_keys: i.ssh_keys ?? [] }),
    confirm: "plain",
    password: true,
    needsInput: true,
  }),
  simple("disable_rescue", "Disable rescue"),
  serverAction("attach_iso", "attach_iso", { label: "Attach ISO", body: (i) => ({ iso: i.iso }), needsInput: true }),
  simple("detach_iso", "Detach ISO"),
  serverAction("attach_to_network", "attach_to_network", {
    label: "Attach to network",
    body: (i) => ({ network: i.network, ...pick(i, ["ip", "alias_ips"]) }),
    needsInput: true,
  }),
  serverAction("detach_from_network", "detach_from_network", {
    label: "Detach",
    body: (i) => ({ network: i.network }),
    confirm: "plain",
  }),
  serverAction("change_alias_ips", "change_alias_ips", {
    label: "Change alias IPs",
    body: (i) => ({ network: i.network, alias_ips: i.alias_ips ?? [] }),
    needsInput: true,
  }),
  serverAction("change_dns_ptr", "change_dns_ptr", {
    label: "Edit reverse DNS",
    body: (i) => ({ ip: i.ip, dns_ptr: i.dns_ptr ?? null }),
    needsInput: true,
  }),
  {
    id: "apply_firewall",
    label: "Apply firewall",
    method: "POST",
    path: (_server, i) => `/firewalls/${i.firewall}/actions/apply_to_resources`,
    body: (_i, server) => ({ apply_to: serverRef(server) }),
    confirm: "none",
    password: false,
    needsInput: true,
  },
  {
    id: "remove_firewall",
    label: "Remove",
    method: "POST",
    path: (_server, i) => `/firewalls/${i.firewall}/actions/remove_from_resources`,
    body: (_i, server) => ({ remove_from: serverRef(server) }),
    confirm: "plain",
    password: false,
    needsInput: false,
  },
  {
    id: "attach_volume",
    label: "Attach volume",
    method: "POST",
    path: (_server, i) => `/volumes/${i.volume}/actions/attach`,
    body: (i, server) => ({ assignee: { id: server, type: "server" }, automount: i.automount === true }),
    confirm: "none",
    password: false,
    needsInput: true,
  },
  {
    id: "detach_volume",
    label: "Detach",
    method: "POST",
    path: (_server, i) => `/volumes/${i.volume}/actions/detach`,
    confirm: "plain",
    password: false,
    needsInput: false,
  },
  serverAction("change_protection", "change_protection", {
    label: "Save",
    body: (i) => pick(i, ["delete", "rebuild"]),
  }),
];

export const tabActions: Record<string, ServerAction> = Object.fromEntries(tabList.map((a) => [a.id, a]));

/** request builds method, path and body; undefined for a disabled entry. */
export function request(
  a: ServerAction,
  server: number,
  input: Input = {},
): { method: string; path: string; body?: unknown } | undefined {
  if (a.disabledHint !== undefined || !a.method || !a.path) return undefined;
  const r: { method: string; path: string; body?: unknown } = { method: a.method, path: a.path(server, input) };
  const body = a.body?.(input, server);
  if (body !== undefined) r.body = body;
  return r;
}
