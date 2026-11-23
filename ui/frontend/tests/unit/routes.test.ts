// Unit tests of the window's routes: href and parse agree for every kind of
// page, unknown routes parse to undefined. They need no browser.
import { test, expect } from "@playwright/test";
import { consoleStart, href, parse, type Route } from "../../src/lib/routes";

test.describe("UI-02 routes", () => {
  const roundTrips: { name: string; route: Route; hash: string }[] = [
    { name: "project list", route: { area: "projects" }, hash: "#/projects" },
    {
      name: "servers list",
      route: { area: "project", project: "demo", view: "servers", page: { kind: "list" } },
      hash: "#/projects/demo/servers",
    },
    {
      name: "servers new",
      route: { area: "project", project: "demo", view: "servers", page: { kind: "new" } },
      hash: "#/projects/demo/servers/new",
    },
    {
      name: "servers detail with tab",
      route: { area: "project", project: "demo", view: "servers", page: { kind: "detail", id: 42, tab: "networking" } },
      hash: "#/projects/demo/servers/42/networking",
    },
    {
      name: "ssh-keys list",
      route: { area: "project", project: "demo", view: "ssh-keys", page: { kind: "list" } },
      hash: "#/projects/demo/ssh-keys",
    },
    {
      name: "ssh-keys new",
      route: { area: "project", project: "demo", view: "ssh-keys", page: { kind: "new" } },
      hash: "#/projects/demo/ssh-keys/new",
    },
    {
      name: "ssh-keys detail with tab",
      route: { area: "project", project: "demo", view: "ssh-keys", page: { kind: "detail", id: 7, tab: "labels" } },
      hash: "#/projects/demo/ssh-keys/7/labels",
    },
    {
      name: "tokens list",
      route: { area: "project", project: "demo", view: "tokens", page: { kind: "list" } },
      hash: "#/projects/demo/tokens",
    },
    { name: "admin server-types", route: { area: "admin", view: "server-types" }, hash: "#/admin/server-types" },
    { name: "admin settings", route: { area: "admin", view: "settings" }, hash: "#/admin/settings" },
    { name: "admin logs", route: { area: "admin", view: "logs" }, hash: "#/admin/logs" },
  ];
  for (const c of roundTrips) {
    test(`href and parse agree: ${c.name}`, () => {
      expect(href(c.route)).toBe(c.hash);
      expect(parse(c.hash)).toEqual(c.route);
    });
  }

  const overview: { name: string; hash: string }[] = [
    { name: "detail without tab", hash: "#/projects/demo/servers/42" },
    { name: "detail with an invalid tab", hash: "#/projects/demo/servers/42/Bad_Tab" },
  ];
  for (const c of overview) {
    test(`overview tab: ${c.name}`, () => {
      expect(parse(c.hash)).toEqual({
        area: "project",
        project: "demo",
        view: "servers",
        page: { kind: "detail", id: 42, tab: "overview" },
      });
    });
  }

  const unknown: { name: string; hash: string }[] = [
    { name: "unknown view", hash: "#/projects/demo/nowhere" },
    { name: "ID that is not a number", hash: "#/projects/demo/servers/abc/overview" },
    { name: "unknown area", hash: "#/nowhere" },
  ];
  for (const c of unknown) {
    test(`undefined: ${c.name}`, () => {
      expect(parse(c.hash)).toBeUndefined();
    });
  }

  test("consoleStart is the Servers list", () => {
    expect(consoleStart("demo")).toEqual({
      area: "project",
      project: "demo",
      view: "servers",
      page: { kind: "list" },
    });
  });
});

test.describe("UI-02 resource routes", () => {
  // Each new view with a tab its detail page has; snapshots and backups have
  // no create page.
  const views: { view: string; tab: string; hasNew: boolean }[] = [
    { view: "volumes", tab: "labels", hasNew: true },
    { view: "load-balancers", tab: "targets", hasNew: true },
    { view: "networks", tab: "subnets", hasNew: true },
    { view: "firewalls", tab: "rules", hasNew: true },
    { view: "floating-ips", tab: "protection", hasNew: true },
    { view: "primary-ips", tab: "labels", hasNew: true },
    { view: "dns", tab: "records", hasNew: true },
    { view: "certificates", tab: "labels", hasNew: true },
    { view: "placement-groups", tab: "labels", hasNew: true },
    { view: "snapshots", tab: "protection", hasNew: false },
    { view: "backups", tab: "labels", hasNew: false },
  ];
  const cases: { name: string; route: Route; hash: string }[] = [];
  for (const v of views) {
    const at = (page: unknown) => ({ area: "project", project: "demo", view: v.view, page }) as Route;
    cases.push({ name: `${v.view} list`, route: at({ kind: "list" }), hash: `#/projects/demo/${v.view}` });
    if (v.hasNew) {
      cases.push({ name: `${v.view} new`, route: at({ kind: "new" }), hash: `#/projects/demo/${v.view}/new` });
    }
    cases.push({
      name: `${v.view} detail with tab`,
      route: at({ kind: "detail", id: 7, tab: v.tab }),
      hash: `#/projects/demo/${v.view}/7/${v.tab}`,
    });
  }
  cases.push({ name: "admin isos", route: { area: "admin", view: "isos" } as Route, hash: "#/admin/isos" });
  for (const c of cases) {
    test(`href and parse agree: ${c.name}`, () => {
      expect(href(c.route)).toBe(c.hash);
      expect(parse(c.hash)).toEqual(c.route);
    });
  }

  for (const view of views.filter((v) => !v.hasNew).map((v) => v.view)) {
    test(`${view} has no create page`, () => {
      expect(parse(`#/projects/demo/${view}/new`)).toBeUndefined();
    });
  }
});

test.describe("UI-12 storage box routes", () => {
  const at = (page: unknown) => ({ area: "project", project: "demo", view: "storage-boxes", page }) as Route;
  const cases: { name: string; route: Route; hash: string }[] = [
    { name: "list", route: at({ kind: "list" }), hash: "#/projects/demo/storage-boxes" },
    { name: "new", route: at({ kind: "new" }), hash: "#/projects/demo/storage-boxes/new" },
  ];
  for (const tab of ["overview", "subaccounts", "snapshots", "settings", "labels"]) {
    cases.push({
      name: `detail ${tab}`,
      route: at({ kind: "detail", id: 7, tab }),
      hash: `#/projects/demo/storage-boxes/7/${tab}`,
    });
  }
  cases.push({
    name: "admin storage-box-types",
    route: { area: "admin", view: "storage-box-types" } as Route,
    hash: "#/admin/storage-box-types",
  });
  for (const c of cases) {
    test(c.name, () => {
      expect(href(c.route)).toBe(c.hash);
      expect(parse(c.hash)).toEqual(c.route);
    });
  }
});
