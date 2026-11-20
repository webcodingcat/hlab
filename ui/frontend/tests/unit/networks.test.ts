// Unit tests of the networks actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/networks";
import { check, id, type Case } from "./resourceTable";

const na = (act: string) => `/networks/${id}/actions/${act}`;

test.describe("UI-02 networks", () => {
  const cases: Case[] = [
    {
      name: "create",
      ref: "create",
      input: { name: "lan", ip_range: "10.0.0.0/16", labels: { env: "lab" } },
      want: { method: "POST", path: "/networks", body: { name: "lan", ip_range: "10.0.0.0/16", labels: { env: "lab" } } },
      confirm: "none",
    },
    {
      name: "change IP range",
      ref: { menu: "Change IP range" },
      input: { ip_range: "10.0.0.0/15" },
      want: { method: "POST", path: na("change_ip_range"), body: { ip_range: "10.0.0.0/15" } },
      confirm: "plain",
    },
    {
      // The subnet is always of type cloud and never sends expose_routes_to_vswitch.
      name: "add subnet",
      ref: { tab: "add_subnet" },
      input: { network_zone: "eu-central", ip_range: "10.0.1.0/24", type: "vswitch", expose_routes_to_vswitch: true },
      want: {
        method: "POST",
        path: na("add_subnet"),
        body: { type: "cloud", network_zone: "eu-central", ip_range: "10.0.1.0/24" },
      },
      confirm: "none",
    },
    {
      name: "delete subnet",
      ref: { tab: "delete_subnet" },
      input: { ip_range: "10.0.1.0/24" },
      want: { method: "POST", path: na("delete_subnet"), body: { ip_range: "10.0.1.0/24" } },
      confirm: "plain",
    },
    {
      name: "add route",
      ref: { tab: "add_route" },
      input: { destination: "10.100.1.0/24", gateway: "10.0.1.1" },
      want: { method: "POST", path: na("add_route"), body: { destination: "10.100.1.0/24", gateway: "10.0.1.1" } },
      confirm: "none",
    },
    {
      name: "delete route",
      ref: { tab: "delete_route" },
      input: { destination: "10.100.1.0/24", gateway: "10.0.1.1" },
      want: { method: "POST", path: na("delete_route"), body: { destination: "10.100.1.0/24", gateway: "10.0.1.1" } },
      confirm: "plain",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: na("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/networks/${id}` }, confirm: "typed-name" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
