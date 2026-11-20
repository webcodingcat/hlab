// Servers: the server types, images and status tones shared by the list,
// the create form and the detail page.

export type Tone = "success" | "warning" | "muted" | "error";

export type Named = { id: number; name: string };

export type Image = {
  id: number;
  type: "system" | "app" | "snapshot" | "backup";
  status: string;
  name: string | null;
  description: string;
  architecture: string;
};

export type ServerType = Named & {
  cores: number;
  memory: number;
  disk: number;
  architecture: string;
  locations: { name: string; available: boolean }[];
};

export type Server = {
  id: number;
  name: string;
  status: string;
  created: string;
  public_net: {
    ipv4: { ip: string; dns_ptr: string } | null;
    ipv6: { ip: string; dns_ptr: { ip: string; dns_ptr: string }[] } | null;
    firewalls: { id: number; status: string }[];
  };
  private_net: { network: number; ip: string; alias_ips: string[] }[];
  server_type: { name: string };
  location: { name: string };
  image: Image | null;
  placement_group: Named | null;
  backup_window: string | null;
  rescue_enabled: boolean;
  iso: (Named & { description: string }) | null;
  protection: { delete: boolean; rebuild: boolean };
  labels: Record<string, string>;
};

// Transitional states (starting, stopping, rebuilding, ...) are warnings.
const tones: Record<string, Tone> = { running: "success", off: "muted", unknown: "error" };

/** tone is the status pill's colour role of a server status. */
export const tone = (status: string): Tone => tones[status] ?? "warning";

/** createdText shows an ISO time as "YYYY-MM-DD HH:MM". */
export const createdText = (created: string) => created.slice(0, 16).replace("T", " ");

/** usableImages keeps the available x86 system images, snapshots and backups. */
export const usableImages = (all: Image[]) =>
  all.filter((x) => x.type !== "app" && x.status === "available" && x.architecture === "x86");

/** imageGroups groups images for a select: system images, snapshots, backups. */
export const imageGroups = (images: Image[]) =>
  (
    [
      ["system", "System images"],
      ["snapshot", "Snapshots"],
      ["backup", "Backups"],
    ] as const
  ).map(([type, label]) => ({ label, images: images.filter((i) => i.type === type) }));

/** imageLabel names a system image by its name, an own image by its description. */
export const imageLabel = (i: Image) => (i.type === "system" ? (i.name ?? `${i.id}`) : i.description || `${i.type} ${i.id}`);

/** typesIn lists the non-ARM types available in a location. */
export const typesIn = (types: ServerType[], location: string) =>
  types.filter((t) => t.architecture !== "arm" && t.locations.some((l) => l.name === location && l.available));

/** typeLabel describes a server type with its size. */
export const typeLabel = (t: ServerType) => `${t.name} (${t.cores} vCPU, ${t.memory} GB RAM, ${t.disk} GB disk)`;
