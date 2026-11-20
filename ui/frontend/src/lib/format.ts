// Text forms of values that many console lists and detail pages show.

/** labelText shows labels as "key=value, ..." sorted by key. */
export function labelText(labels: Record<string, string>): string {
  return Object.keys(labels ?? {})
    .sort()
    .map((k) => `${k}=${labels[k]}`)
    .join(", ");
}

/** createdText shows an API timestamp as "YYYY-MM-DD hh:mm". */
export function createdText(created: string): string {
  return created.slice(0, 16).replace("T", " ");
}

/** protocolText shows an IP type as IPv4 or IPv6. */
export const protocolText = (t: string) => (t === "ipv6" ? "IPv6" : "IPv4");
