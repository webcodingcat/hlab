// Project list logic: sorting, filtering and the resource counts of a card.
// Pure: imports neither the bridge nor the Wails runtime, so unit tests load it.
import type { ProjectView } from "./routes";

/** Project as GET /projects lists it. */
export type Project = {
  name: string;
  display_name: string;
  created: string; // RFC 3339
  resources: number;
  resources_by_type: Record<string, number>;
};
export type SortKey = "display_name" | "created";
export type Direction = "asc" | "desc";
export type Layout = "grid" | "list";
export type ProjectSort = { key: SortKey; dir: Direction };

/** sortProjects returns a sorted copy; ties go by name in the same direction. */
export function sortProjects(ps: Project[], sort: ProjectSort): Project[] {
  const sign = sort.dir === "asc" ? 1 : -1;
  const primary =
    sort.key === "display_name"
      ? (a: Project, b: Project) => a.display_name.localeCompare(b.display_name, undefined, { sensitivity: "base" })
      : (a: Project, b: Project) => Date.parse(a.created) - Date.parse(b.created);
  const byName = (a: Project, b: Project) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
  return [...ps].sort((a, b) => sign * (primary(a, b) || byName(a, b)));
}

/** filterProjects keeps projects whose display name or name contains query (case-insensitive). */
export function filterProjects(ps: Project[], query: string): Project[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...ps];
  return ps.filter((p) => p.display_name.toLowerCase().includes(q) || p.name.toLowerCase().includes(q));
}

export type ResourceEntry = { key: string; count: number; label: string; view: ProjectView };

// The keys of resources_by_type in sidebar order with their view and noun.
const kinds: { key: string; view: ProjectView; one: string; many: string }[] = [
  { key: "servers", view: "servers", one: "Server", many: "Servers" },
  { key: "snapshots", view: "snapshots", one: "Snapshot", many: "Snapshots" },
  { key: "backups", view: "backups", one: "Backup", many: "Backups" },
  { key: "placement_groups", view: "placement-groups", one: "Placement Group", many: "Placement Groups" },
  { key: "volumes", view: "volumes", one: "Volume", many: "Volumes" },
  { key: "storage_boxes", view: "storage-boxes", one: "Storage Box", many: "Storage Boxes" },
  { key: "firewalls", view: "firewalls", one: "Firewall", many: "Firewalls" },
  { key: "load_balancers", view: "load-balancers", one: "Load Balancer", many: "Load Balancers" },
  { key: "networks", view: "networks", one: "Network", many: "Networks" },
  { key: "primary_ips", view: "primary-ips", one: "Primary IP", many: "Primary IPs" },
  { key: "floating_ips", view: "floating-ips", one: "Floating IP", many: "Floating IPs" },
  { key: "zones", view: "dns", one: "DNS Zone", many: "DNS Zones" },
  { key: "ssh_keys", view: "ssh-keys", one: "SSH Key", many: "SSH Keys" },
  { key: "certificates", view: "certificates", one: "Certificate", many: "Certificates" },
];

/** resourceEntries lists the counts above zero in sidebar order; unknown keys are skipped. */
export function resourceEntries(byType: Record<string, number>): ResourceEntry[] {
  const out: ResourceEntry[] = [];
  for (const k of kinds) {
    const n = byType[k.key] ?? 0;
    if (n > 0) out.push({ key: k.key, count: n, label: `${n} ${n === 1 ? k.one : k.many}`, view: k.view });
  }
  return out;
}

/** resourcesText is "1 resource" or "<n> resources". */
export function resourcesText(n: number): string {
  return `${n} ${n === 1 ? "resource" : "resources"}`;
}
