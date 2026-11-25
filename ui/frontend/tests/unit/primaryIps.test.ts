// Unit tests of the primaryIps actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test, expect } from "@playwright/test";
import * as mod from "../../src/lib/resources/primaryIps";
import { request } from "../../src/lib/resourceActions";
import { check, id, type Case } from "./resourceTable";

const pa = (act: string) => `/primary_ips/${id}/actions/${act}`;

test.describe("UI-02 primary IPs", () => {
  const cases: Case[] = [
    {
      // A primary IP takes a location, never a datacenter.
      name: "create without a server",
      ref: "create",
      input: { name: "ip1", type: "ipv4", location: "fsn1", auto_delete: false, labels: { env: "lab" } },
      want: {
        method: "POST",
        path: "/primary_ips",
        body: { name: "ip1", type: "ipv4", location: "fsn1", auto_delete: false, labels: { env: "lab" } },
      },
      confirm: "none",
    },
    {
      // With a server to assign, the server's location applies.
      name: "create with a server",
      ref: "create",
      input: { name: "ip1", type: "ipv4", location: "fsn1", assignee_id: 5, auto_delete: true, labels: {} },
      want: {
        method: "POST",
        path: "/primary_ips",
        body: { name: "ip1", type: "ipv4", assignee_type: "server", assignee_id: 5, auto_delete: true, labels: {} },
      },
      confirm: "none",
    },
    {
      name: "assign",
      ref: { menu: "Assign" },
      input: { assignee_id: 5 },
      want: { method: "POST", path: pa("assign"), body: { assignee_type: "server", assignee_id: 5 } },
      confirm: "none",
    },
    { name: "unassign", ref: { menu: "Unassign" }, want: { method: "POST", path: pa("unassign") }, confirm: "plain" },
    {
      name: "auto_delete on",
      ref: { tab: "auto_delete" },
      input: { auto_delete: true },
      want: { method: "PUT", path: `/primary_ips/${id}`, body: { auto_delete: true } },
      confirm: "none",
    },
    {
      name: "auto_delete off",
      ref: { tab: "auto_delete" },
      input: { auto_delete: false },
      want: { method: "PUT", path: `/primary_ips/${id}`, body: { auto_delete: false } },
      confirm: "none",
    },
    {
      name: "reverse DNS set",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.30", dns_ptr: "ip1.example.com" },
      want: { method: "POST", path: pa("change_dns_ptr"), body: { ip: "203.0.113.30", dns_ptr: "ip1.example.com" } },
      confirm: "none",
    },
    {
      name: "reverse DNS reset",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.30", dns_ptr: "" },
      want: { method: "POST", path: pa("change_dns_ptr"), body: { ip: "203.0.113.30", dns_ptr: null } },
      confirm: "none",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: pa("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/primary_ips/${id}` }, confirm: "typed-name" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});

test("UI-09 row menu attach/assign", () => {
  // An unassigned primary IP offers Assign, which opens the server select.
  const assign = mod.rowAction({ assignee_type: "server", assignee_id: null });
  expect(assign.label).toBe("Assign");
  expect(assign.needsInput).toBe(true);
  expect(assign.confirm).toBe("none");
  expect(request(assign, id, { assignee_id: 5 })?.path).toBe(pa("assign"));
  // An assigned primary IP offers Unassign with its plain confirm.
  const unassign = mod.rowAction({ assignee_type: "server", assignee_id: 5 });
  expect(unassign.label).toBe("Unassign");
  expect(unassign.confirm).toBe("plain");
  expect(request(unassign, id, {})?.path).toBe(pa("unassign"));
});
