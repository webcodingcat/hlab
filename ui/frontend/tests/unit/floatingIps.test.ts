// Unit tests of the floatingIps actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/floatingIps";
import { check, id, type Case } from "./resourceTable";

const fa = (act: string) => `/floating_ips/${id}/actions/${act}`;

test.describe("UI-02 floating IPs", () => {
  const cases: Case[] = [
    {
      name: "create with a home location",
      ref: "create",
      input: { type: "ipv4", home_location: "fsn1", name: "vip", description: "web", labels: { env: "lab" } },
      want: {
        method: "POST",
        path: "/floating_ips",
        body: { type: "ipv4", home_location: "fsn1", name: "vip", description: "web", labels: { env: "lab" } },
      },
      confirm: "none",
    },
    {
      name: "create with a server",
      ref: "create",
      input: { type: "ipv6", server: 5, name: "vip6", labels: {} },
      want: { method: "POST", path: "/floating_ips", body: { type: "ipv6", server: 5, name: "vip6", labels: {} } },
      confirm: "none",
    },
    {
      name: "assign",
      ref: { menu: "Assign" },
      input: { server: 5 },
      want: { method: "POST", path: fa("assign"), body: { server: 5 } },
      confirm: "none",
    },
    { name: "unassign", ref: { menu: "Unassign" }, want: { method: "POST", path: fa("unassign") }, confirm: "plain" },
    {
      name: "reverse DNS set",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.20", dns_ptr: "vip.example.com" },
      want: { method: "POST", path: fa("change_dns_ptr"), body: { ip: "203.0.113.20", dns_ptr: "vip.example.com" } },
      confirm: "none",
    },
    {
      name: "reverse DNS reset",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.20", dns_ptr: "" },
      want: { method: "POST", path: fa("change_dns_ptr"), body: { ip: "203.0.113.20", dns_ptr: null } },
      confirm: "none",
    },
    {
      name: "description",
      ref: { tab: "description" },
      input: { description: "web front" },
      want: { method: "PUT", path: `/floating_ips/${id}`, body: { description: "web front" } },
      confirm: "none",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: fa("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/floating_ips/${id}` }, confirm: "plain" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
