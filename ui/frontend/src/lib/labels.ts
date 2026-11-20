// Label rows of the forms and the Labels tab, their conversion to the API's
// labels object, and the parsing and checking of a typed label.

export type LabelRow = { key: string; value: string };

/** toRows lists labels as rows sorted by key. */
export function toRows(labels: Record<string, string>): LabelRow[] {
  return Object.keys(labels)
    .sort()
    .map((key) => ({ key, value: labels[key] }));
}

/**
 * toLabels skips rows with an empty key and value, keeps empty values and
 * refuses a key that occurs twice, naming the index of its second row.
 */
export function toLabels(rows: LabelRow[]): { labels: Record<string, string> } | { error: string; row: number } {
  const labels: Record<string, string> = {};
  for (const [i, r] of rows.entries()) {
    const key = r.key;
    if (key === "" && r.value === "") continue;
    if (key in labels) return { error: `Label key "${key}" is used twice.`, row: i };
    labels[key] = r.value;
  }
  return { labels };
}

const reservedPrefix = "hetzner.cloud/";
// name and value: alphanumerics at start and end, -_. and alphanumerics between.
const namePattern = /^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$/;
// one DNS label of a DNS subdomain.
const dnsLabelPattern = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
const nameRule = "at most 63 characters, start and end with an alphanumeric character, only dashes, underscores and dots in between";

function validName(s: string): boolean {
  return s.length <= 63 && namePattern.test(s);
}

function validDnsSubdomain(s: string): boolean {
  return s !== "" && s.length <= 253 && s.split(".").every((p) => p.length <= 63 && dnsLabelPattern.test(p));
}

/**
 * checkLabel applies the API's label rule (internal/labels): an optional
 * DNS-subdomain prefix "x/" before the key name, key name and value at most
 * 63 characters, alphanumeric at both ends and -_. inside, the value
 * optional and the prefix hetzner.cloud/ reserved. It returns the message
 * of the first broken rule.
 */
export function checkLabel(row: LabelRow): string | undefined {
  const { key, value } = row;
  if (key.startsWith(reservedPrefix)) return `Label key "${key}": the prefix "${reservedPrefix}" is reserved.`;
  let name = key;
  const slash = key.indexOf("/");
  if (slash >= 0) {
    name = key.slice(slash + 1);
    if (!validDnsSubdomain(key.slice(0, slash)))
      return `Label key "${key}": the prefix must be a DNS subdomain of at most 253 characters.`;
  }
  if (!validName(name)) return `Label key "${key}" is invalid: ${nameRule}.`;
  if (value !== "" && !validName(value)) return `Label value "${value}" is invalid: ${nameRule}.`;
  return undefined;
}

/** parseLabel reads "key=value", "key=" or "key", trimmed, and checks it. */
export function parseLabel(text: string): { row: LabelRow } | { error: string } {
  const t = text.trim();
  const eq = t.indexOf("=");
  const row = eq < 0 ? { key: t, value: "" } : { key: t.slice(0, eq), value: t.slice(eq + 1) };
  const error = checkLabel(row);
  return error ? { error } : { row };
}
