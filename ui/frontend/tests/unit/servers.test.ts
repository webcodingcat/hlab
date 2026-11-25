// Unit tests of the server helpers the Create server form and "Change type"
// share: the preselected location and the server types offered for a
// location with their labels. They need no browser.
import { test, expect } from "@playwright/test";
import * as servers from "../../src/views/servers/servers";
import type { ServerType } from "../../src/views/servers/servers";

const deprecation = { announced: "2025-10-16T06:00:00Z", unavailable_after: "2025-12-31T23:59:59Z" };

/** st builds a server type with one entry per location. */
function st(
  id: number,
  name: string,
  architecture: string,
  locations: { name: string; available: boolean; deprecation?: unknown }[],
): ServerType {
  return {
    id,
    name,
    cores: 2,
    memory: 4,
    disk: 40,
    architecture,
    locations: locations.map((l) => ({ deprecation: null, ...l })),
  } as ServerType;
}

test("UI-09 server type choice", async () => {
  await test.step("the form preselects the location with the lowest ID", () => {
    const locations = [
      { id: 4, name: "ash" },
      { id: 2, name: "nbg1" },
      { id: 1, name: "fsn1" },
      { id: 3, name: "hel1" },
    ];
    expect(servers.defaultLocation(locations)).toBe("fsn1");
  });

  const types = [
    st(1, "cx23", "x86", [{ name: "nbg1", available: false }]),
    st(2, "cpx11", "x86", [{ name: "nbg1", available: false, deprecation }]),
    st(3, "ccx13", "x86", [{ name: "nbg1", available: true }]),
    st(4, "cax11", "arm", [{ name: "nbg1", available: true }]),
    st(5, "cpx12", "x86", [{ name: "ash", available: true }]),
  ];

  await test.step("every non-ARM type with an entry for the location is offered, available or not", () => {
    const names = servers.typesIn(types, "nbg1").map((t) => t.name);
    expect(names.sort()).toEqual(["ccx13", "cpx11", "cx23"]);
  });

  await test.step("a type deprecated in the location is labelled (deprecated)", () => {
    const cpx11 = types[1];
    const ccx13 = types[2];
    expect(servers.typeLabel(cpx11, "nbg1")).toContain("cpx11");
    expect(servers.typeLabel(cpx11, "nbg1")).toMatch(/ \(deprecated\)$/);
    expect(servers.typeLabel(ccx13, "nbg1")).toContain("ccx13");
    expect(servers.typeLabel(ccx13, "nbg1")).not.toContain("deprecated");
  });
});
