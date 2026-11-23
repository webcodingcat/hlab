// Unit tests of the project list logic: the sort orders of the project
// cards and the resource counts per type in a card's footer. They need no
// browser.
import { test, expect } from "@playwright/test";
import { sortProjects, resourceEntries, resourcesText, type Project, type ProjectSort } from "../../src/lib/projects";

function project(name: string, display_name: string, created: string): Project {
  return { name, display_name, created, resources: 0, resources_by_type: {} };
}

test.describe("UI-11 sort", () => {
  const alpha = project("alpha", "Alpha", "2026-03-01T10:00:00Z");
  const bravo = project("bravo", "bravo", "2026-01-01T10:00:00Z");
  const charlie = project("charlie", "Charlie", "2026-02-01T10:00:00Z");
  // Same display name as alpha but for case, and the same created as charlie.
  const alpha2 = project("alpha-2", "ALPHA", "2026-02-01T10:00:00Z");

  const cases: { name: string; input: Project[]; sort: ProjectSort; want: string[] }[] = [
    {
      name: "display name ascending, case-insensitive",
      input: [charlie, bravo, alpha],
      sort: { key: "display_name", dir: "asc" },
      want: ["alpha", "bravo", "charlie"],
    },
    {
      name: "display name descending, case-insensitive",
      input: [alpha, charlie, bravo],
      sort: { key: "display_name", dir: "desc" },
      want: ["charlie", "bravo", "alpha"],
    },
    {
      name: "created ascending",
      input: [alpha, charlie, bravo],
      sort: { key: "created", dir: "asc" },
      want: ["bravo", "charlie", "alpha"],
    },
    {
      name: "created descending",
      input: [bravo, alpha, charlie],
      sort: { key: "created", dir: "desc" },
      want: ["alpha", "charlie", "bravo"],
    },
    {
      name: "equal display names ascending by name",
      input: [alpha2, bravo, alpha],
      sort: { key: "display_name", dir: "asc" },
      want: ["alpha", "alpha-2", "bravo"],
    },
    {
      name: "equal display names descending by name",
      input: [alpha, bravo, alpha2],
      sort: { key: "display_name", dir: "desc" },
      want: ["bravo", "alpha-2", "alpha"],
    },
    {
      name: "equal created ascending by name",
      input: [charlie, alpha2, bravo],
      sort: { key: "created", dir: "asc" },
      want: ["bravo", "alpha-2", "charlie"],
    },
    {
      name: "equal created descending by name",
      input: [alpha2, charlie, bravo],
      sort: { key: "created", dir: "desc" },
      want: ["charlie", "alpha-2", "bravo"],
    },
  ];
  for (const c of cases) {
    test(c.name, () => {
      const before = structuredClone(c.input);
      const got = sortProjects(c.input, c.sort);
      expect(got.map((p) => p.name)).toEqual(c.want);
      expect(c.input, "the input stays unchanged").toEqual(before);
    });
  }
});

test.describe("UI-11 resources", () => {
  // Every key the API sends, in sidebar order, with its view and noun.
  const kinds: { key: string; view: string; one: string; many: string }[] = [
    { key: "servers", view: "servers", one: "Server", many: "Servers" },
    { key: "snapshots", view: "snapshots", one: "Snapshot", many: "Snapshots" },
    { key: "backups", view: "backups", one: "Backup", many: "Backups" },
    { key: "placement_groups", view: "placement-groups", one: "Placement Group", many: "Placement Groups" },
    { key: "volumes", view: "volumes", one: "Volume", many: "Volumes" },
    { key: "firewalls", view: "firewalls", one: "Firewall", many: "Firewalls" },
    { key: "load_balancers", view: "load-balancers", one: "Load Balancer", many: "Load Balancers" },
    { key: "networks", view: "networks", one: "Network", many: "Networks" },
    { key: "primary_ips", view: "primary-ips", one: "Primary IP", many: "Primary IPs" },
    { key: "floating_ips", view: "floating-ips", one: "Floating IP", many: "Floating IPs" },
    { key: "zones", view: "dns", one: "DNS Zone", many: "DNS Zones" },
    { key: "ssh_keys", view: "ssh-keys", one: "SSH Key", many: "SSH Keys" },
    { key: "certificates", view: "certificates", one: "Certificate", many: "Certificates" },
  ];
  // The keys in reverse order, so the sidebar order must come from resourceEntries.
  const all = (n: number) => Object.fromEntries([...kinds].reverse().map((k) => [k.key, n]));

  const entryCases: { name: string; input: Record<string, number>; want: { key: string; count: number; label: string; view: string }[] }[] = [
    {
      name: "one of each in sidebar order, singular",
      input: all(1),
      want: kinds.map((k) => ({ key: k.key, count: 1, label: `1 ${k.one}`, view: k.view })),
    },
    {
      name: "two of each in sidebar order, plural",
      input: all(2),
      want: kinds.map((k) => ({ key: k.key, count: 2, label: `2 ${k.many}`, view: k.view })),
    },
    {
      name: "unknown keys and zero counts left out",
      input: { isos: 3, images: 1, volumes: 0, servers: 0, ssh_keys: 1 },
      want: [{ key: "ssh_keys", count: 1, label: "1 SSH Key", view: "ssh-keys" }],
    },
    { name: "no counts", input: {}, want: [] },
  ];
  for (const c of entryCases) {
    test(c.name, () => {
      expect(resourceEntries(c.input)).toEqual(c.want);
    });
  }

  const textCases: { n: number; want: string }[] = [
    { n: 1, want: "1 resource" },
    { n: 2, want: "2 resources" },
  ];
  for (const c of textCases) {
    test(`resourcesText ${c.n}`, () => {
      expect(resourcesText(c.n)).toBe(c.want);
    });
  }
});

test.describe("UI-12 storage box count", () => {
  test("storage boxes after volumes", () => {
    // The keys out of sidebar order, so the place must come from resourceEntries.
    expect(resourceEntries({ firewalls: 1, storage_boxes: 2, volumes: 1 })).toEqual([
      { key: "volumes", count: 1, label: "1 Volume", view: "volumes" },
      { key: "storage_boxes", count: 2, label: "2 Storage Boxes", view: "storage-boxes" },
      { key: "firewalls", count: 1, label: "1 Firewall", view: "firewalls" },
    ]);
  });
});
