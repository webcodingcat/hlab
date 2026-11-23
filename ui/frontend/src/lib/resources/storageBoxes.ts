// Storage box actions: the create form's request, the detail page's actions
// menu and the actions of the detail tabs, plus the types a location offers.
import { createAt, pick, postAction, remove, rename, update, type ResourceAction } from "../resourceActions";

const base = "storage_boxes";

export type BoxType = { id: number; name: string; prices: { location: string }[]; deprecation: unknown | null };

/** StorageBox is the storage box object of the Hetzner API. */
export type StorageBox = {
  id: number;
  name: string;
  username: string | null;
  server: string | null;
  status: string;
  storage_box_type: BoxType;
  location: { name: string };
  access_settings: Record<string, boolean>;
  snapshot_plan: Record<string, number | null> | null;
  protection: { delete: boolean };
  stats: { size: number; size_data: number; size_snapshots: number };
  labels: Record<string, string>;
};

/** create sends name, location, type, password, SSH public keys, access settings and labels. */
export const create: ResourceAction = createAt(base, "Create storage box", (i) =>
  pick(i, ["name", "location", "storage_box_type", "password", "ssh_keys", "access_settings", "labels"]),
);

const changeType = postAction(base, "change_type", "change_type", {
  label: "Change type",
  body: (i) => ({ storage_box_type: i.storage_box_type }),
  needsInput: true,
});

const resetPassword = postAction(base, "reset_password", "reset_password", {
  label: "Reset password",
  body: (i) => ({ password: i.password }),
  needsInput: true,
});

const updateAccess = postAction(base, "update_access_settings", "update_access_settings", {
  label: "Update access settings",
  body: (i) => pick(i, ["reachable_externally", "samba_enabled", "ssh_enabled", "webdav_enabled", "zfs_enabled"]),
  needsInput: true,
});

const protection = postAction(base, "change_protection", "change_protection", {
  label: "Change protection",
  body: (i) => ({ delete: i.delete }),
  needsInput: true,
});

const labels = update(base, "labels", "Labels", (i) => ({ labels: i.labels }), { needsInput: true });

const del = remove(base, "Delete", "typed-name");

/** menu is the detail page's actions menu and the list row menu, Delete last. */
export const menu: ResourceAction[] = [rename(base), del];

/** tabs are the actions of the detail tabs. */
export const tabs: Record<string, ResourceAction> = {
  change_type: changeType,
  reset_password: resetPassword,
  update_access_settings: updateAccess,
  change_protection: protection,
  labels,
  delete: del,
};

/** typesIn lists the types with a price entry for location, in catalog order. */
export function typesIn(types: BoxType[], location: string): BoxType[] {
  return types.filter((t) => t.prices.some((p) => p.location === location));
}
