// Unit tests of the storage box action modules: every entry of the box,
// subaccount and snapshot modules calls the right endpoint with the right
// body and carries the right confirmation; the types offered per location,
// the snapshot plan body and the snapshots a rollback deletes. They need no
// browser.
import { test, expect } from "@playwright/test";
import * as boxes from "../../src/lib/resources/storageBoxes";
import * as subaccounts from "../../src/lib/resources/storageBoxSubaccounts";
import * as snapshots from "../../src/lib/resources/storageBoxSnapshots";
import { check, id, type Case, type ResourceModule } from "./resourceTable";

const ba = (act: string) => `/storage_boxes/${id}/actions/${act}`;

test.describe("UI-12 storage boxes", () => {
  const access = {
    reachable_externally: true,
    samba_enabled: false,
    ssh_enabled: true,
    webdav_enabled: false,
    zfs_enabled: true,
  };
  const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample lab";
  const cases: Case[] = [
    {
      name: "create",
      ref: "create",
      input: {
        name: "backup",
        location: "fsn1",
        storage_box_type: "bx11",
        password: "Secret-Pass-123!",
        ssh_keys: [key],
        access_settings: access,
        labels: { env: "lab" },
      },
      want: {
        method: "POST",
        path: "/storage_boxes",
        body: {
          name: "backup",
          location: "fsn1",
          storage_box_type: "bx11",
          password: "Secret-Pass-123!",
          ssh_keys: [key],
          access_settings: access,
          labels: { env: "lab" },
        },
      },
      confirm: "none",
    },
    {
      name: "rename",
      ref: { menu: "Rename" },
      input: { name: "archive" },
      want: { method: "PUT", path: `/storage_boxes/${id}`, body: { name: "archive" } },
      confirm: "none",
    },
    {
      name: "change type",
      ref: { tab: "change_type" },
      input: { storage_box_type: "bx21" },
      want: { method: "POST", path: ba("change_type"), body: { storage_box_type: "bx21" } },
      confirm: "none",
    },
    {
      name: "reset password",
      ref: { tab: "reset_password" },
      input: { password: "Other-Pass-456!" },
      want: { method: "POST", path: ba("reset_password"), body: { password: "Other-Pass-456!" } },
      confirm: "none",
    },
    {
      name: "update access settings",
      ref: { tab: "update_access_settings" },
      input: access,
      want: { method: "POST", path: ba("update_access_settings"), body: access },
      confirm: "none",
    },
    {
      name: "change protection",
      ref: { tab: "change_protection" },
      input: { delete: true },
      want: { method: "POST", path: ba("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    {
      name: "labels",
      ref: { tab: "labels" },
      input: { labels: { env: "prod" } },
      want: { method: "PUT", path: `/storage_boxes/${id}`, body: { labels: { env: "prod" } } },
      confirm: "none",
    },
    {
      name: "delete from the menu",
      ref: { menu: "Delete" },
      want: { method: "DELETE", path: `/storage_boxes/${id}` },
      confirm: "typed-name",
    },
    {
      name: "delete from the detail",
      ref: { tab: "delete" },
      want: { method: "DELETE", path: `/storage_boxes/${id}` },
      confirm: "typed-name",
    },
  ];
  for (const c of cases) test(c.name, () => check(boxes, c));
});

test.describe("UI-12 types per location", () => {
  const type = (tid: number, name: string, locations: string[]): boxes.BoxType => ({
    id: tid,
    name,
    prices: locations.map((location) => ({ location })),
    deprecation: null,
  });
  // Catalog order, not sorted by name or ID.
  const catalog = [type(2, "bx21", ["fsn1"]), type(1, "bx11", ["fsn1", "hel1"]), type(3, "bx31", ["hel1"])];
  const cases: { name: string; location: string; want: string[] }[] = [
    { name: "fsn1 in catalog order", location: "fsn1", want: ["bx21", "bx11"] },
    { name: "hel1 in catalog order", location: "hel1", want: ["bx11", "bx31"] },
    { name: "a location no type has", location: "nbg1", want: [] },
  ];
  for (const c of cases) {
    test(c.name, () => {
      expect(boxes.typesIn(catalog, c.location).map((t) => t.name)).toEqual(c.want);
    });
  }
});

// The subaccount and snapshot modules have only detail actions.
const subMod: ResourceModule = { menu: [], tabs: subaccounts.tabs };
const snapMod: ResourceModule = { menu: [], tabs: snapshots.tabs };

test.describe("UI-13 subaccounts", () => {
  const sub = 3;
  const sa = `/storage_boxes/${id}/subaccounts/${sub}`;
  const subAccess = {
    reachable_externally: false,
    readonly: true,
    samba_enabled: true,
    ssh_enabled: true,
    webdav_enabled: false,
  };
  const cases: Case[] = [
    {
      name: "create",
      ref: { tab: "create" },
      input: {
        home_directory: "team/a",
        password: "Secret-Pass-123!",
        name: "team-a",
        description: "Team A",
        access_settings: subAccess,
      },
      want: {
        method: "POST",
        path: `/storage_boxes/${id}/subaccounts`,
        body: {
          home_directory: "team/a",
          password: "Secret-Pass-123!",
          name: "team-a",
          description: "Team A",
          access_settings: subAccess,
        },
      },
      confirm: "none",
    },
    {
      name: "update name and description",
      ref: { tab: "update" },
      input: { subaccount: sub, name: "team-b", description: "Team B" },
      want: { method: "PUT", path: sa, body: { name: "team-b", description: "Team B" } },
      confirm: "none",
    },
    {
      name: "change home directory",
      ref: { tab: "change_home_directory" },
      input: { subaccount: sub, home_directory: "team/b" },
      want: { method: "POST", path: `${sa}/actions/change_home_directory`, body: { home_directory: "team/b" } },
      confirm: "none",
    },
    {
      name: "reset password",
      ref: { tab: "reset_password" },
      input: { subaccount: sub, password: "Other-Pass-456!" },
      want: { method: "POST", path: `${sa}/actions/reset_subaccount_password`, body: { password: "Other-Pass-456!" } },
      confirm: "none",
    },
    {
      name: "update access settings",
      ref: { tab: "update_access_settings" },
      input: { subaccount: sub, ...subAccess },
      want: { method: "POST", path: `${sa}/actions/update_access_settings`, body: subAccess },
      confirm: "none",
    },
    {
      name: "delete",
      ref: { tab: "delete" },
      input: { subaccount: sub },
      want: { method: "DELETE", path: sa },
      confirm: "plain",
    },
  ];
  for (const c of cases) test(c.name, () => check(subMod, c));
});

test.describe("UI-13 snapshots", () => {
  const snap = 9;
  const sn = `/storage_boxes/${id}/snapshots/${snap}`;
  const plan = { max_snapshots: 5, minute: 30, hour: 3, day_of_week: 1, day_of_month: null };
  const cases: Case[] = [
    {
      name: "create",
      ref: { tab: "create" },
      input: { description: "before upgrade" },
      want: { method: "POST", path: `/storage_boxes/${id}/snapshots`, body: { description: "before upgrade" } },
      confirm: "none",
    },
    {
      name: "update description",
      ref: { tab: "update" },
      input: { snapshot: snap, description: "after upgrade" },
      want: { method: "PUT", path: sn, body: { description: "after upgrade" } },
      confirm: "none",
    },
    {
      name: "delete",
      ref: { tab: "delete" },
      input: { snapshot: snap },
      want: { method: "DELETE", path: sn },
      confirm: "plain",
    },
    {
      name: "rollback",
      ref: { tab: "rollback" },
      input: { snapshot: snap },
      want: { method: "POST", path: ba("rollback_snapshot"), body: { snapshot: snap } },
      confirm: "plain",
    },
    {
      name: "enable plan",
      ref: { tab: "enable_plan" },
      input: plan,
      want: { method: "POST", path: ba("enable_snapshot_plan"), body: plan },
      confirm: "none",
    },
    {
      name: "disable plan",
      ref: { tab: "disable_plan" },
      want: { method: "POST", path: ba("disable_snapshot_plan") },
      confirm: "plain",
    },
  ];
  for (const c of cases) test(c.name, () => check(snapMod, c));
});

test.describe("UI-13 plan and rollback", () => {
  const base = { max_snapshots: 7, hour: 2, minute: 45 };
  const planCases: {
    name: string;
    input: snapshots.PlanInput;
    want: { day_of_week: number | null; day_of_month: number | null };
  }[] = [
    {
      name: "planBody every day",
      input: { ...base, day_of_week: "every", day_of_month: "every" },
      want: { day_of_week: null, day_of_month: null },
    },
    {
      name: "planBody Monday",
      input: { ...base, day_of_week: 1, day_of_month: "every" },
      want: { day_of_week: 1, day_of_month: null },
    },
    {
      name: "planBody Sunday",
      input: { ...base, day_of_week: 7, day_of_month: "every" },
      want: { day_of_week: 7, day_of_month: null },
    },
    {
      name: "planBody day of month",
      input: { ...base, day_of_week: "every", day_of_month: 15 },
      want: { day_of_week: null, day_of_month: 15 },
    },
  ];
  for (const c of planCases) {
    test(c.name, () => {
      expect(snapshots.planBody(c.input)).toEqual({ ...base, ...c.want });
    });
  }

  // Not in creation order, so newerThan must sort.
  const s = (sid: number, created: string): snapshots.Snap => ({ id: sid, name: `snap-${sid}`, created });
  const first = s(1, "2026-11-01T10:00:00Z");
  const second = s(2, "2026-11-02T10:00:00Z");
  const third = s(3, "2026-11-03T10:00:00Z");
  const fourth = s(4, "2026-11-04T10:00:00Z");
  const list = [third, first, fourth, second];
  const newerCases: { name: string; target: snapshots.Snap; want: number[] }[] = [
    { name: "newerThan the oldest", target: first, want: [2, 3, 4] },
    { name: "newerThan a middle one", target: second, want: [3, 4] },
    { name: "newerThan the newest", target: fourth, want: [] },
  ];
  for (const c of newerCases) {
    test(c.name, () => {
      expect(snapshots.newerThan(list, c.target).map((x) => x.id)).toEqual(c.want);
    });
  }
});
