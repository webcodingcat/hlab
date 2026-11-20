// Unit tests of the firewalls actions module and the rules conversion: every
// entry of the create form, the actions menu and the detail tabs calls the
// right endpoint with the right body and carries the right confirmation, and
// the rules editor rows convert to API rules and back. They need no browser.
import { test, expect } from "@playwright/test";
import * as mod from "../../src/lib/resources/firewalls";
import { toRuleRows, toRules, type Rule, type RuleRow } from "../../src/lib/resources/firewallRules";
import { check, id, type Case } from "./resourceTable";

const fw = (act: string) => `/firewalls/${id}/actions/${act}`;

test.describe("UI-02 firewalls", () => {
  const rules: Rule[] = [
    { direction: "in", protocol: "tcp", port: "22", source_ips: ["0.0.0.0/0"], destination_ips: [] },
  ];
  const server = { type: "server", server: { id: 5 } };
  const selector = { type: "label_selector", label_selector: { selector: "env=lab" } };
  const cases: Case[] = [
    {
      name: "create with rules and apply_to",
      ref: "create",
      input: { name: "web", labels: { env: "lab" }, rules, apply_to: [server, selector] },
      want: {
        method: "POST",
        path: "/firewalls",
        body: { name: "web", labels: { env: "lab" }, rules, apply_to: [server, selector] },
      },
      confirm: "none",
    },
    {
      name: "set rules",
      ref: { tab: "set_rules" },
      input: { rules },
      want: { method: "POST", path: fw("set_rules"), body: { rules } },
      confirm: "none",
    },
    {
      name: "apply to a server",
      ref: { tab: "apply_to_server" },
      input: { server: 5 },
      want: { method: "POST", path: fw("apply_to_resources"), body: { apply_to: [server] } },
      confirm: "none",
    },
    {
      name: "apply to a label selector",
      ref: { tab: "apply_to_label_selector" },
      input: { label_selector: "env=lab" },
      want: { method: "POST", path: fw("apply_to_resources"), body: { apply_to: [selector] } },
      confirm: "none",
    },
    {
      name: "remove from a server",
      ref: { tab: "remove_from_server" },
      input: { server: 5 },
      want: { method: "POST", path: fw("remove_from_resources"), body: { remove_from: [server] } },
      confirm: "plain",
    },
    {
      name: "remove from a label selector",
      ref: { tab: "remove_from_label_selector" },
      input: { label_selector: "env=lab" },
      want: { method: "POST", path: fw("remove_from_resources"), body: { remove_from: [selector] } },
      confirm: "plain",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/firewalls/${id}` }, confirm: "plain" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});

test.describe("UI-02 firewall rules", () => {
  /** ipList splits an ips field at commas and line breaks. */
  const ipList = (ips: string) =>
    ips
      .split(/[,\n]/)
      .map((s) => s.trim())
      .filter((s) => s !== "");

  // row is what the rules editor holds, rule what toRules makes of it, back
  // the row toRuleRows gives for the rule (the row itself unless a field was dropped).
  const cases: { name: string; row: RuleRow; rule: Rule; back?: RuleRow }[] = [
    {
      name: "inbound tcp",
      row: { direction: "in", protocol: "tcp", port: "22", ips: "0.0.0.0/0, ::/0", description: "ssh" },
      rule: {
        direction: "in",
        protocol: "tcp",
        port: "22",
        source_ips: ["0.0.0.0/0", "::/0"],
        destination_ips: [],
        description: "ssh",
      },
    },
    {
      name: "outbound udp split at line breaks without description",
      row: { direction: "out", protocol: "udp", port: "53", ips: "10.0.0.0/8,\n192.168.0.0/16\n", description: "" },
      rule: {
        direction: "out",
        protocol: "udp",
        port: "53",
        source_ips: [],
        destination_ips: ["10.0.0.0/8", "192.168.0.0/16"],
      },
    },
    {
      name: "icmp drops the port",
      row: { direction: "in", protocol: "icmp", port: "80", ips: "0.0.0.0/0", description: "ping" },
      rule: { direction: "in", protocol: "icmp", source_ips: ["0.0.0.0/0"], destination_ips: [], description: "ping" },
      back: { direction: "in", protocol: "icmp", port: "", ips: "0.0.0.0/0", description: "ping" },
    },
    {
      name: "outbound gre",
      row: { direction: "out", protocol: "gre", port: "", ips: "203.0.113.0/24", description: "tunnel" },
      rule: {
        direction: "out",
        protocol: "gre",
        source_ips: [],
        destination_ips: ["203.0.113.0/24"],
        description: "tunnel",
      },
    },
  ];
  for (const c of cases) {
    test(c.name, () => {
      expect(toRules([c.row])).toEqual([c.rule]);
      const back = toRuleRows([c.rule]);
      const want = c.back ?? c.row;
      expect(back).toHaveLength(1);
      expect({ ...back[0], ips: ipList(back[0].ips) }).toEqual({ ...want, ips: ipList(want.ips) });
    });
  }
});
