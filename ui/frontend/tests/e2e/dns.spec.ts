import { expect, api, uniqueName } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, named, openRow, addLabel, deleteRow } from "./console";

type Zone = { id: number; name: string; mode: string; labels: Record<string, string> };

test("UI-02 dns", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = `${uniqueName("ui02")}.internal`;
  await openConsole(page, shared.name);
  await section(page, "DNS");
  await helpOpens(page);

  // "Add zone" creates an empty primary zone.
  await page.getByRole("button", { name: "Add zone", exact: true }).click();
  await page.getByLabel("Domain", { exact: true }).fill(name);
  // The form's submit button carries the same name as the one that opened it.
  await page.getByRole("button", { name: "Add zone", exact: true }).last().click();
  await expect.poll(async () => (await named<Zone>(token, "/zones", "zones", name)).length, { timeout: 60_000 }).toBe(1);
  const [created] = await named<Zone>(token, "/zones", "zones", name);
  expect(created.mode).toBe("primary");
  // Its create action locks it until it ends (API-13); Delete waits for that.
  await expect
    .poll(
      async () => ((await api(token, `/zones/${created.id}/actions?status=running`)).body as { actions: unknown[] }).actions.length,
      { timeout: 60_000 },
    )
    .toBe(0);
  await section(page, "DNS");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect.poll(async () => (await named<Zone>(token, "/zones", "zones", name))[0]?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });

  // Delete needs the name typed and removes it from the list and the API.
  await section(page, "DNS");
  await deleteRow(page, name, true);
  await expect.poll(async () => (await api(token, `/zones/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
