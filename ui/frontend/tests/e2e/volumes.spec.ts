import { expect, env, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, named, openRow, addLabel, deleteRow } from "./console";

type Volume = { id: number; name: string; size: number; server: number | null; location: { name: string }; labels: Record<string, string> };

test("UI-02 volumes", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-vol";
  const location = env("HLAB_E2E_SERVER_LOCATION");
  await openConsole(page, shared.name);
  await section(page, "Volumes");
  await helpOpens(page);

  // The form creates a volume with Size, Name and Location, without a server.
  await page.getByRole("button", { name: "Create volume", exact: true }).click();
  await page.getByLabel("Size", { exact: true }).fill("10");
  await page.getByLabel("Name", { exact: true }).fill(name);
  await pick(page, "Location", location);
  await page.getByRole("button", { name: /^Create( volume)?$/ }).last().click();
  await expect.poll(async () => (await named<Volume>(token, "/volumes", "volumes", name)).length, { timeout: 2 * 60_000 }).toBe(1);
  const [created] = await named<Volume>(token, "/volumes", "volumes", name);
  expect(created.size).toBe(10);
  expect(created.server).toBeNull();
  expect(created.location.name).toBe(location);
  await section(page, "Volumes");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect.poll(async () => (await named<Volume>(token, "/volumes", "volumes", name))[0]?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });

  // Delete needs the name typed and removes it from the list and the API.
  await section(page, "Volumes");
  await deleteRow(page, name, true);
  await expect.poll(async () => (await api(token, `/volumes/${created.id}`)).status, { timeout: 2 * 60_000 }).toBe(404);
});
