// Label rows of the forms and the Labels tab, and their conversion to the
// API's labels object.

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
