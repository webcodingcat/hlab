// Unit tests of the zones actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/zones";
import { check, id, type Case } from "./resourceTable";

const za = (act: string) => `/zones/${id}/actions/${act}`;
const rr = `/zones/${id}/rrsets/www/A`;
const rrSet = { rr_name: "www", rr_type: "A" };

test.describe("UI-02 zones", () => {
  const zonefile = "$ORIGIN example.com.\n$TTL 3600\nwww IN A 203.0.113.10\n";
  const nameservers = [{ address: "198.51.100.53", port: 53 }];
  const cases: Case[] = [
    {
      name: "create empty",
      ref: "create",
      input: { name: "example.com", mode: "primary", ttl: 3600, labels: { env: "lab" } },
      want: {
        method: "POST",
        path: "/zones",
        body: { name: "example.com", mode: "primary", ttl: 3600, labels: { env: "lab" } },
      },
      confirm: "none",
    },
    {
      name: "create with zone file",
      ref: "create",
      input: { name: "example.com", mode: "primary", zonefile, labels: {} },
      want: { method: "POST", path: "/zones", body: { name: "example.com", mode: "primary", zonefile, labels: {} } },
      confirm: "none",
    },
    {
      name: "create secondary",
      ref: "create",
      input: { name: "example.com", mode: "secondary", primary_nameservers: nameservers, labels: {} },
      want: {
        method: "POST",
        path: "/zones",
        body: { name: "example.com", mode: "secondary", primary_nameservers: nameservers, labels: {} },
      },
      confirm: "none",
    },
    {
      name: "change default TTL",
      ref: { menu: "Change default TTL" },
      input: { ttl: 600 },
      want: { method: "POST", path: za("change_ttl"), body: { ttl: 600 } },
      confirm: "none",
    },
    {
      name: "import zone file",
      ref: { menu: "Import zone file" },
      input: { zonefile },
      want: { method: "POST", path: za("import_zonefile"), body: { zonefile } },
      confirm: "plain",
    },
    {
      name: "export zone file",
      ref: { tab: "export_zonefile" },
      want: { method: "GET", path: `/zones/${id}/zonefile` },
      confirm: "none",
    },
    {
      name: "change primary nameservers",
      ref: { tab: "change_primary_nameservers" },
      input: { primary_nameservers: nameservers },
      want: { method: "POST", path: za("change_primary_nameservers"), body: { primary_nameservers: nameservers } },
      confirm: "none",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: za("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/zones/${id}` }, confirm: "typed-name" },
    {
      name: "RRSet create",
      ref: { tab: "create_rrset" },
      input: { name: "www", type: "A", ttl: 300, records: [{ value: "203.0.113.10" }] },
      want: {
        method: "POST",
        path: `/zones/${id}/rrsets`,
        body: { name: "www", type: "A", ttl: 300, records: [{ value: "203.0.113.10" }] },
      },
      confirm: "none",
    },
    {
      name: "RRSet change TTL",
      ref: { tab: "rrset_change_ttl" },
      input: { ...rrSet, ttl: 600 },
      want: { method: "POST", path: `${rr}/actions/change_ttl`, body: { ttl: 600 } },
      confirm: "none",
    },
    {
      name: "RRSet add records",
      ref: { tab: "rrset_add_records" },
      input: { ...rrSet, records: [{ value: "203.0.113.11" }] },
      want: { method: "POST", path: `${rr}/actions/add_records`, body: { records: [{ value: "203.0.113.11" }] } },
      confirm: "none",
    },
    {
      name: "RRSet remove records",
      ref: { tab: "rrset_remove_records" },
      input: { ...rrSet, records: [{ value: "203.0.113.11" }] },
      want: { method: "POST", path: `${rr}/actions/remove_records`, body: { records: [{ value: "203.0.113.11" }] } },
      confirm: "plain",
    },
    {
      name: "RRSet labels",
      ref: { tab: "rrset_labels" },
      input: { ...rrSet, labels: { env: "lab" } },
      want: { method: "PUT", path: rr, body: { labels: { env: "lab" } } },
      confirm: "none",
    },
    {
      name: "RRSet change protection",
      ref: { tab: "rrset_change_protection" },
      input: { ...rrSet, change: true },
      want: { method: "POST", path: `${rr}/actions/change_protection`, body: { change: true } },
      confirm: "none",
    },
    {
      name: "RRSet delete",
      ref: { tab: "rrset_delete" },
      input: rrSet,
      want: { method: "DELETE", path: rr },
      confirm: "typed-name",
    },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
