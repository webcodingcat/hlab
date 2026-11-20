// The project console's navigation: the sidebar groups with their entries
// and the section tabs of each entry. Pure, so the unit tests load it
// without a browser.
import type { ProjectView } from "./routes";

/** NavTab is one tab of an entry's tab bar. */
export type NavTab = { view: ProjectView; title: string };
/** NavEntry is one sidebar entry; it opens its first tab. */
export type NavEntry = { id: string; title: string; tabs: NavTab[] };
/** NavGroup is one sidebar group; the security group has no heading. */
export type NavGroup = { id: "cloud" | "networking" | "security"; title?: string; entries: NavEntry[] };

export const navGroups: NavGroup[] = [
  {
    id: "cloud",
    title: "Cloud",
    entries: [
      {
        id: "servers",
        title: "Servers",
        tabs: [
          { view: "servers", title: "Servers" },
          { view: "snapshots", title: "Snapshots" },
          { view: "backups", title: "Backups" },
          { view: "placement-groups", title: "Placement Groups" },
        ],
      },
      { id: "volumes", title: "Volumes", tabs: [{ view: "volumes", title: "Volumes" }] },
      { id: "firewalls", title: "Firewalls", tabs: [{ view: "firewalls", title: "Firewalls" }] },
    ],
  },
  {
    id: "networking",
    title: "Networking",
    entries: [
      { id: "load-balancers", title: "Load Balancers", tabs: [{ view: "load-balancers", title: "Load Balancers" }] },
      { id: "networks", title: "Networks", tabs: [{ view: "networks", title: "Networks" }] },
      {
        id: "ips",
        title: "IPs",
        tabs: [
          { view: "primary-ips", title: "Primary IPs" },
          { view: "floating-ips", title: "Floating IPs" },
        ],
      },
      { id: "dns", title: "DNS", tabs: [{ view: "dns", title: "DNS Zones" }] },
    ],
  },
  {
    id: "security",
    entries: [
      {
        id: "security",
        title: "Security",
        tabs: [
          { view: "ssh-keys", title: "SSH Keys" },
          { view: "tokens", title: "API tokens" },
          { view: "certificates", title: "Certificates" },
        ],
      },
    ],
  },
];

/**
 * entryOf is the sidebar entry whose tabs hold view. A view no tab holds
 * (the isos view until it leaves the console) belongs to Servers.
 */
export function entryOf(view: ProjectView): NavEntry {
  const entries = navGroups.flatMap((g) => g.entries);
  return entries.find((e) => e.tabs.some((t) => t.view === view)) ?? entries[0];
}
