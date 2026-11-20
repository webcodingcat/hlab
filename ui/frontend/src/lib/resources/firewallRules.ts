// Firewall rules: conversion between the rows of the rules editor (one row per
// rule, the IPs as one text field) and the rules of the API. Pure, so the
// unit tests load it without a browser.

export type RuleRow = {
  direction: "in" | "out";
  protocol: "tcp" | "udp" | "icmp" | "esp" | "gre";
  port: string;
  ips: string; // addresses separated by commas or line breaks
  description: string;
};

export type Rule = {
  direction: "in" | "out";
  protocol: string;
  port?: string;
  source_ips: string[];
  destination_ips: string[];
  description?: string;
};

/** splitIps splits an IPs field at commas and line breaks and drops empty parts. */
function splitIps(ips: string): string[] {
  return ips
    .split(/[,\r\n]/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

/** toRules turns editor rows into API rules: in -> source_ips, out -> destination_ips; port only for tcp/udp. */
export function toRules(rows: RuleRow[]): Rule[] {
  return rows.map((r) => {
    const ips = splitIps(r.ips);
    const rule: Rule = {
      direction: r.direction,
      protocol: r.protocol,
      source_ips: r.direction === "in" ? ips : [],
      destination_ips: r.direction === "out" ? ips : [],
    };
    if (r.protocol === "tcp" || r.protocol === "udp") rule.port = r.port.trim();
    const description = r.description.trim();
    if (description !== "") rule.description = description;
    return rule;
  });
}

/** toRuleRows turns API rules back into editor rows. */
export function toRuleRows(rules: Rule[]): RuleRow[] {
  return rules.map((r) => ({
    direction: r.direction,
    protocol: r.protocol as RuleRow["protocol"],
    port: r.port ?? "",
    ips: (r.direction === "in" ? r.source_ips : r.destination_ips).join(", "),
    description: r.description ?? "",
  }));
}
