import type { Locator, Page } from "@playwright/test";
import { test, expect, env, api, bound, openView, rowAction, tableRow, uniqueName } from "./fixtures";
import { newProject, pick } from "./console";

type Iso = { id: number; name: string | null; description: string; type: string };
type IsoPage = { isos: Iso[]; meta?: { pagination?: { next_page: number | null } } };
type ListedISO = { iso: Iso; source: string; own?: { id: number; description: string } };

const bridge = "github.com/webcodingcat/hlab/internal/bridge.Service";

/** allIsos lists every ISO a project's API lists, across its pages. */
async function allIsos(token: string): Promise<Iso[]> {
  const isos: Iso[] = [];
  for (let p: number | null = 1; p !== null; ) {
    const r = await api(token, `/isos?page=${p}&per_page=50`);
    if (r.status !== 200) throw new Error(`GET /isos: status ${r.status}`);
    const body = r.body as IsoPage;
    isos.push(...body.isos);
    p = body.meta?.pagination?.next_page ?? null;
  }
  return isos;
}

/** servedAs gives the type of each ISO /isos lists with this description (own ISOs have no name). */
async function servedAs(token: string, description: string): Promise<string[]> {
  return (await allIsos(token)).filter((i) => i.name === null && i.description === description).map((i) => i.type);
}

/** openIsos opens the Administration view ISOs through its sidebar. */
async function openIsos(page: Page): Promise<void> {
  await openView(page, "Server Types");
  await page.getByRole("navigation").getByRole("link", { name: "ISOs", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "ISOs", exact: true })).toBeVisible();
}

/** addIso fills the Add ISO form with Name and Architecture, lets source fill the source, and adds it. */
async function addIso(page: Page, name: string, source: (form: Locator) => Promise<void>): Promise<void> {
  await page.getByRole("button", { name: "Add ISO", exact: true }).click();
  const form = page.getByRole("dialog");
  await form.getByLabel("Name", { exact: true }).fill(name);
  await pick(page, "Architecture", "x86");
  await source(form);
  await form.getByRole("button", { name: "Add", exact: true }).click();
  await expect(form).toBeHidden({ timeout: 30_000 });
}

test("UI-10 isos", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  const file = env("HLAB_E2E_ISO_FILE");
  const project = await newProject("e2e-ui10");
  const prefix = uniqueName("ui10");
  try {
    const token = project.token;
    await openIsos(page);

    // The catalog ISOs are listed with their source.
    const catalog = (await allIsos(token)).find((i) => i.name !== null);
    expect(catalog, "the API lists a catalog ISO").toBeDefined();
    const catalogRow = tableRow(page, catalog!.name!);
    await expect(catalogRow).toBeVisible({ timeout: 30_000 });
    await expect(catalogRow.getByRole("cell", { name: "catalog", exact: true })).toBeVisible();

    // Add ISO with a URL and SHA-256: listed as own and served as private.
    const byUrl = `${prefix}-url`;
    await addIso(page, byUrl, async (form) => {
      await pick(page, "Source", "URL");
      await form.getByLabel("URL", { exact: true }).fill(`https://example.com/${byUrl}.iso`);
      await form.getByLabel("SHA-256", { exact: true }).fill("ab".repeat(32));
    });
    const urlRow = tableRow(page, byUrl);
    await expect(urlRow).toBeVisible({ timeout: 30_000 });
    await expect(urlRow.getByRole("cell", { name: "own", exact: true })).toBeVisible();
    await expect.poll(() => servedAs(token, byUrl), { timeout: 60_000 }).toEqual(["private"]);

    // Add ISO with a local .iso path: the same way.
    const byPath = `${prefix}-file`;
    await addIso(page, byPath, async (form) => {
      await pick(page, "Source", "Local file");
      await form.getByLabel("Path", { exact: true }).fill(file);
    });
    const pathRow = tableRow(page, byPath);
    await expect(pathRow).toBeVisible({ timeout: 30_000 });
    await expect(pathRow.getByRole("cell", { name: "own", exact: true })).toBeVisible();
    await expect.poll(() => servedAs(token, byPath), { timeout: 60_000 }).toEqual(["private"]);

    // Edit changes the Name in the table and in /isos.
    const renamed = `${prefix}-renamed`;
    await rowAction(urlRow, "Edit");
    const edit = page.getByRole("dialog");
    await expect(edit.getByLabel("Name", { exact: true })).toHaveValue(byUrl);
    await edit.getByLabel("Name", { exact: true }).fill(renamed);
    await edit.getByRole("button", { name: "Save", exact: true }).click();
    await expect(edit).toBeHidden({ timeout: 30_000 });
    await expect(tableRow(page, renamed)).toBeVisible({ timeout: 30_000 });
    await expect(tableRow(page, byUrl)).toBeHidden();
    await expect.poll(() => servedAs(token, renamed), { timeout: 60_000 }).toEqual(["private"]);
    expect(await servedAs(token, byUrl)).toEqual([]);

    // Remove after the confirmation removes both from the table and /isos.
    for (const name of [renamed, byPath]) {
      const row = tableRow(page, name);
      await rowAction(row, "Remove");
      await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
      await expect(row).toBeHidden({ timeout: 30_000 });
      await expect.poll(() => servedAs(token, name), { timeout: 60_000 }).toEqual([]);
    }
  } finally {
    // Every own ISO this test added goes, also after a failure.
    const list = await bound<{ isos: ListedISO[] }>(page, `${bridge}.ISOs`);
    for (const l of list.isos) {
      if (l.own && l.own.description.startsWith(prefix)) await bound<unknown>(page, `${bridge}.RemoveISO`, l.own.id);
    }
    await project.remove();
  }
});
