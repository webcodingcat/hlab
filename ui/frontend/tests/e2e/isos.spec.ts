import { expect, api, tableRow } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens } from "./console";

type IsoPage = { isos: { name: string }[]; meta?: { pagination?: { next_page: number | null } } };

/** isoNames lists the names of all ISOs the API lists, across its pages. */
async function isoNames(token: string): Promise<string[]> {
  const names: string[] = [];
  for (let p: number | null = 1; p !== null; ) {
    const r = await api(token, `/isos?page=${p}&per_page=50`);
    if (r.status !== 200) throw new Error(`GET /isos: status ${r.status}`);
    const body = r.body as IsoPage;
    names.push(...body.isos.map((i) => i.name));
    p = body.meta?.pagination?.next_page ?? null;
  }
  return names;
}

test("UI-02 isos", async ({ page, shared }) => {
  test.setTimeout(5 * 60_000);
  const names = await isoNames(shared.token);
  await openConsole(page, shared.name);
  await section(page, "ISOs");
  await helpOpens(page);

  // The section lists the ISOs the API lists, by name.
  for (const name of names) {
    await expect(tableRow(page, name), `ISO ${name} is listed`).toBeVisible({ timeout: 30_000 });
  }
});
