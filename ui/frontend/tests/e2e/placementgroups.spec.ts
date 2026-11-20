import { expect, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, named, openRow, addLabel, deleteRow } from "./console";

type PlacementGroup = { id: number; name: string; type: string; labels: Record<string, string> };

test("UI-02 placement groups", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-pg";
  await openConsole(page, shared.name);
  await section(page, "Placement Groups");
  await helpOpens(page);

  // The form creates a spread placement group.
  await page.getByRole("button", { name: "Create placement group", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: /^Create( placement group)?$/ }).last().click();
  await expect
    .poll(async () => (await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name)).length, { timeout: 60_000 })
    .toBe(1);
  const [created] = await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name);
  expect(created.type).toBe("spread");
  await section(page, "Placement Groups");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect
    .poll(async () => (await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name))[0]?.labels, { timeout: 30_000 })
    .toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Placement Groups");
  await deleteRow(page, name, false);
  await expect.poll(async () => (await api(token, `/placement_groups/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
