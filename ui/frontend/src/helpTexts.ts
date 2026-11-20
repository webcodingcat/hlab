// The help texts src/help/<view>.md, keyed by view ID.
const files = import.meta.glob<string>("./help/*.md", { query: "?raw", import: "default", eager: true });

export const helpTexts: Record<string, string> = Object.fromEntries(
  Object.entries(files).map(([path, text]) => [path.replace(/^\.\/help\//, "").replace(/\.md$/, ""), text]),
);
