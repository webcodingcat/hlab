// Text forms of values that many console lists and detail pages show.

/** labelText shows labels as "key=value, ..." sorted by key. */
export function labelText(labels: Record<string, string>): string {
  return Object.keys(labels ?? {})
    .sort()
    .map((k) => `${k}=${labels[k]}`)
    .join(", ");
}

/** createdText shows an API timestamp in local time as "YYYY-MM-DD hh:mm"; an unparsable value is shown as given. */
export function createdText(created: string): string {
  const d = new Date(created);
  if (isNaN(d.getTime())) return created;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** protocolText shows an IP type as IPv4 or IPv6. */
export const protocolText = (t: string) => (t === "ipv6" ? "IPv6" : "IPv4");

/** actionTitle is the toast title of an action on a named resource: "<Type> <name>: <action>". */
export function actionTitle(type: string, name: string, action: string): string {
  return `${[type, name].filter((s) => s !== "").join(" ")}: ${action}`;
}

// The resource type of each collection of the API paths the console sends.
const types: Record<string, string> = {
  servers: "Server",
  volumes: "Volume",
  firewalls: "Firewall",
  images: "Image",
  placement_groups: "Placement group",
  load_balancers: "Load balancer",
  networks: "Network",
  primary_ips: "Primary IP",
  floating_ips: "Floating IP",
  zones: "Zone",
  certificates: "Certificate",
  ssh_keys: "SSH key",
};

/** resourceType names the resource type of an API path, e.g. "/networks/5/actions/x" -> "Network"; RRSets are "Record". */
export function resourceType(path: string): string {
  const parts = path.split("/").filter((p) => p !== "");
  if (parts[0] === "zones" && parts[2] === "rrsets") return "Record";
  return types[parts[0]] ?? "";
}

/** isoTitle is the shown name of an ISO: its name, or the description of an own ISO (name null). */
export function isoTitle(iso: { name: string | null; description: string }): string {
  return iso.name ?? iso.description;
}
