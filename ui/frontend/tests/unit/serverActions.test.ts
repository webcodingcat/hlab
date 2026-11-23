// Unit tests of the server actions module: every entry of the actions menu
// and of the detail tabs calls the right endpoint with the right body and
// carries the right confirmation and password flags. They need no browser.
import { test, expect } from "@playwright/test";
import { menuActions, request, tabActions, type Input, type ServerAction } from "../../src/lib/serverActions";

const server = 42;

/** entry finds a menu entry by its label or a tab action by its id. */
function entry(ref: { menu: string } | { tab: string }): ServerAction {
  if ("menu" in ref) {
    const a = menuActions.find((x) => x.label === ref.menu);
    if (!a) throw new Error(`no menu entry labelled ${ref.menu}`);
    return a;
  }
  const a = tabActions[ref.tab];
  if (!a) throw new Error(`no tab action ${ref.tab}`);
  return a;
}

/** sa is the path of a server action. */
const sa = (action: string) => `/servers/${server}/actions/${action}`;

type Ref = { menu: string } | { tab: string };
type Want = { method: string; path: string; body?: unknown };

test.describe("UI-02 requests", () => {
  const cases: { name: string; ref: Ref; input?: Input; want: Want }[] = [
    { name: "power on", ref: { menu: "Power on" }, want: { method: "POST", path: sa("poweron") } },
    { name: "shutdown", ref: { menu: "Shutdown" }, want: { method: "POST", path: sa("shutdown") } },
    { name: "reboot", ref: { menu: "Reboot" }, want: { method: "POST", path: sa("reboot") } },
    { name: "reset", ref: { menu: "Reset" }, want: { method: "POST", path: sa("reset") } },
    { name: "power off", ref: { menu: "Power off" }, want: { method: "POST", path: sa("poweroff") } },
    {
      name: "reset root password",
      ref: { menu: "Reset root password" },
      want: { method: "POST", path: sa("reset_password") },
    },
    {
      name: "rebuild",
      ref: { menu: "Rebuild" },
      input: { image: "ubuntu-24.04" },
      want: { method: "POST", path: sa("rebuild"), body: { image: "ubuntu-24.04" } },
    },
    {
      name: "change type with upgrade_disk",
      ref: { menu: "Change type" },
      input: { server_type: "cx32", upgrade_disk: true },
      want: { method: "POST", path: sa("change_type"), body: { server_type: "cx32", upgrade_disk: true } },
    },
    {
      name: "rename",
      ref: { menu: "Rename" },
      input: { name: "web-2" },
      want: { method: "PUT", path: `/servers/${server}`, body: { name: "web-2" } },
    },
    {
      name: "add to placement group",
      ref: { menu: "Add to placement group" },
      input: { placement_group: 5 },
      want: { method: "POST", path: sa("add_to_placement_group"), body: { placement_group: 5 } },
    },
    {
      name: "remove from placement group",
      ref: { menu: "Remove from placement group" },
      want: { method: "POST", path: sa("remove_from_placement_group") },
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/servers/${server}` } },
    { name: "backups on", ref: { tab: "enable_backup" }, want: { method: "POST", path: sa("enable_backup") } },
    { name: "backups off", ref: { tab: "disable_backup" }, want: { method: "POST", path: sa("disable_backup") } },
    {
      name: "create backup",
      ref: { tab: "create_backup" },
      want: { method: "POST", path: sa("create_image"), body: { type: "backup" } },
    },
    {
      name: "create snapshot",
      ref: { tab: "create_snapshot" },
      input: { description: "before upgrade" },
      want: { method: "POST", path: sa("create_image"), body: { type: "snapshot", description: "before upgrade" } },
    },
    {
      name: "restore",
      ref: { tab: "restore" },
      input: { image: 77 },
      want: { method: "POST", path: sa("rebuild"), body: { image: 77 } },
    },
    {
      name: "delete image",
      ref: { tab: "delete_image" },
      input: { image: 77 },
      want: { method: "DELETE", path: "/images/77" },
    },
    {
      name: "rescue on",
      ref: { tab: "enable_rescue" },
      input: { ssh_keys: [3, 4] },
      want: { method: "POST", path: sa("enable_rescue"), body: { type: "linux64", ssh_keys: [3, 4] } },
    },
    { name: "rescue off", ref: { tab: "disable_rescue" }, want: { method: "POST", path: sa("disable_rescue") } },
    {
      name: "ISO attach",
      ref: { tab: "attach_iso" },
      input: { iso: "virtio-win-0.1.248.iso" },
      want: { method: "POST", path: sa("attach_iso"), body: { iso: "virtio-win-0.1.248.iso" } },
    },
    { name: "ISO detach", ref: { tab: "detach_iso" }, want: { method: "POST", path: sa("detach_iso") } },
    {
      name: "network attach",
      ref: { tab: "attach_to_network" },
      input: { network: 9, ip: "10.0.1.5", alias_ips: ["10.0.1.6"] },
      want: {
        method: "POST",
        path: sa("attach_to_network"),
        body: { network: 9, ip: "10.0.1.5", alias_ips: ["10.0.1.6"] },
      },
    },
    {
      name: "network detach",
      ref: { tab: "detach_from_network" },
      input: { network: 9 },
      want: { method: "POST", path: sa("detach_from_network"), body: { network: 9 } },
    },
    {
      name: "alias IPs",
      ref: { tab: "change_alias_ips" },
      input: { network: 9, alias_ips: ["10.0.1.7", "10.0.1.8"] },
      want: { method: "POST", path: sa("change_alias_ips"), body: { network: 9, alias_ips: ["10.0.1.7", "10.0.1.8"] } },
    },
    {
      name: "reverse DNS set",
      ref: { tab: "change_dns_ptr" },
      input: { ip: "203.0.113.10", dns_ptr: "web.example.com" },
      want: { method: "POST", path: sa("change_dns_ptr"), body: { ip: "203.0.113.10", dns_ptr: "web.example.com" } },
    },
    {
      name: "reverse DNS reset",
      ref: { tab: "change_dns_ptr" },
      input: { ip: "203.0.113.10", dns_ptr: null },
      want: { method: "POST", path: sa("change_dns_ptr"), body: { ip: "203.0.113.10", dns_ptr: null } },
    },
    {
      name: "firewall apply",
      ref: { tab: "apply_firewall" },
      input: { firewall: 11 },
      want: {
        method: "POST",
        path: "/firewalls/11/actions/apply_to_resources",
        body: { apply_to: [{ type: "server", server: { id: server } }] },
      },
    },
    {
      name: "firewall remove",
      ref: { tab: "remove_firewall" },
      input: { firewall: 11 },
      want: {
        method: "POST",
        path: "/firewalls/11/actions/remove_from_resources",
        body: { remove_from: [{ type: "server", server: { id: server } }] },
      },
    },
    {
      name: "volume attach",
      ref: { tab: "attach_volume" },
      input: { volume: 13, automount: true },
      want: { method: "POST", path: "/volumes/13/actions/attach", body: { assignee: { id: server, type: "server" }, automount: true },
      },
    },
    {
      name: "volume detach",
      ref: { tab: "detach_volume" },
      input: { volume: 13 },
      want: { method: "POST", path: "/volumes/13/actions/detach" },
    },
    {
      name: "protection",
      ref: { tab: "change_protection" },
      input: { delete: true, rebuild: false },
      want: { method: "POST", path: sa("change_protection"), body: { delete: true, rebuild: false } },
    },
  ];
  for (const c of cases) {
    test(`request: ${c.name}`, () => {
      const got = request(entry(c.ref), server, c.input ?? {});
      expect(got).toBeDefined();
      expect(got!.method).toBe(c.want.method);
      expect(got!.path).toBe(c.want.path);
      if (c.want.body === undefined) {
        // An action without parameters may send no body or an empty object.
        expect([undefined, null, {}]).toContainEqual(got!.body);
      } else {
        expect(got!.body).toEqual(c.want.body);
      }
    });
  }

  test("request: console is enabled and builds no request", () => {
    const console = entry({ menu: "Console" });
    expect(console.disabledHint).toBeUndefined();
    expect(request(console, server, {})).toBeUndefined();
  });
});

test.describe("UI-02 confirmation", () => {
  const confirms: { name: string; ref: Ref; confirm: ServerAction["confirm"] }[] = [
    { name: "power off", ref: { menu: "Power off" }, confirm: "plain" },
    { name: "reset", ref: { menu: "Reset" }, confirm: "plain" },
    { name: "rebuild", ref: { menu: "Rebuild" }, confirm: "plain" },
    { name: "change type", ref: { menu: "Change type" }, confirm: "plain" },
    { name: "enable rescue", ref: { tab: "enable_rescue" }, confirm: "plain" },
    { name: "restore", ref: { tab: "restore" }, confirm: "plain" },
    { name: "delete image", ref: { tab: "delete_image" }, confirm: "plain" },
    { name: "delete", ref: { menu: "Delete" }, confirm: "typed-name" },
    { name: "power on", ref: { menu: "Power on" }, confirm: "none" },
    { name: "shutdown", ref: { menu: "Shutdown" }, confirm: "none" },
    { name: "reboot", ref: { menu: "Reboot" }, confirm: "none" },
  ];
  for (const c of confirms) {
    test(`confirm: ${c.name}`, () => {
      expect(entry(c.ref).confirm).toBe(c.confirm);
    });
  }

  test("confirm: only delete needs the name typed", () => {
    const typed = [...menuActions, ...Object.values(tabActions)].filter((a) => a.confirm === "typed-name");
    expect(typed).toEqual([entry({ menu: "Delete" })]);
  });

  test("password: set exactly for reset root password, rebuild, restore and enable rescue", () => {
    const want = new Set<ServerAction>([
      entry({ menu: "Reset root password" }),
      entry({ menu: "Rebuild" }),
      entry({ tab: "restore" }),
      entry({ tab: "enable_rescue" }),
    ]);
    for (const a of [...menuActions, ...Object.values(tabActions)]) {
      expect(a.password, `password flag of ${a.id}`).toBe(want.has(a));
    }
  });
});
