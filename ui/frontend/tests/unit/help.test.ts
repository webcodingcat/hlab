// Unit tests of the help texts and their renderer. They need no browser:
// the help index is loaded through Vite, so its import.meta.glob runs as in
// the app build.
import { fileURLToPath } from "node:url";
import { runnerImport } from "vite";
import { test, expect } from "@playwright/test";
import { helpFor, renderHelp } from "../../src/help";

const rootURL = new URL("../../", import.meta.url);
const root = fileURLToPath(rootURL);

/** viteImport loads a module of ui/frontend through Vite, as the app build does. */
async function viteImport<T>(path: string): Promise<T> {
  const { module } = await runnerImport<T>(fileURLToPath(new URL(path, rootURL)), {
    root,
    configFile: false,
    logLevel: "silent",
  });
  return module;
}

/** Collapses whitespace between tags so the checks ignore formatting. */
function tidy(html: string): string {
  return html.replace(/>\s+</g, "><").trim();
}

test.describe("UI-07", () => {
  test("helpFor gives every view a rendered help text with a heading", async () => {
    const views = (await viteImport<{ default: string[] }>("src/views.json")).default;
    const { helpTexts } = await viteImport<{ helpTexts: Record<string, string> }>("src/helpTexts.ts");
    expect(views.length).toBeGreaterThan(0);
    for (const view of views) {
      const html = helpFor(view, helpTexts);
      expect(html, `help text of view ${view}`).toBeDefined();
      expect(html!, `help text of view ${view}`).toMatch(/<h[1-6][^>]*>[^<]+<\/h[1-6]>/);
    }
  });

  const cases: { name: string; markdown: string; want: RegExp[] }[] = [
    { name: "heading", markdown: "# Projects", want: [/<h1[^>]*>Projects<\/h1>/] },
    { name: "subheading", markdown: "## Create a token", want: [/<h2[^>]*>Create a token<\/h2>/] },
    {
      name: "paragraphs",
      markdown: "First line\nsame paragraph.\n\nSecond paragraph.",
      want: [/<p>First line\s+same paragraph\.<\/p>/, /<p>Second paragraph\.<\/p>/],
    },
    {
      name: "list",
      markdown: "- Start cloud\n- Stop cloud",
      want: [/<ul><li>Start cloud<\/li><li>Stop cloud<\/li><\/ul>/],
    },
    { name: "inline code", markdown: "Run `hlab cloud status`.", want: [/<code>hlab cloud status<\/code>/] },
    {
      name: "code block",
      markdown: "```\nhlab project create demo\nhlab project list\n```",
      want: [/<pre[^>]*><code[^>]*>hlab project create demo\nhlab project list\n?<\/code><\/pre>/],
    },
    { name: "bold", markdown: "This is **never** stored.", want: [/<strong>never<\/strong>/] },
    {
      name: "link",
      markdown: "See [the guide](https://example.com/guide).",
      want: [/<a [^>]*href="https:\/\/example\.com\/guide"[^>]*>the guide<\/a>/],
    },
  ];
  for (const c of cases) {
    test(`renderHelp: ${c.name}`, () => {
      const html = c.name === "code block" ? renderHelp(c.markdown) : tidy(renderHelp(c.markdown));
      for (const re of c.want) expect(html).toMatch(re);
    });
  }
});
