import { expect, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, named, openRow, addLabel, deleteRow } from "./console";

type Network = { id: number; name: string; ip_range: string; labels: Record<string, string> };

test("UI-02 networks", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-net";
  await openConsole(page, shared.name);
  await section(page, "Networks");
  await helpOpens(page);

  // The form creates a network with Name and IP range.
  await page.getByRole("button", { name: "Create network", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByLabel("IP range", { exact: true }).fill("10.42.0.0/16");
  await page.getByRole("button", { name: /^Create( network)?$/ }).last().click();
  await expect.poll(async () => (await named<Network>(token, "/networks", "networks", name)).length, { timeout: 60_000 }).toBe(1);
  const [created] = await named<Network>(token, "/networks", "networks", name);
  expect(created.ip_range).toBe("10.42.0.0/16");
  await section(page, "Networks");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect.poll(async () => (await named<Network>(token, "/networks", "networks", name))[0]?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });

  // Delete needs the name typed and removes it from the list and the API.
  await section(page, "Networks");
  await deleteRow(page, name);
  await expect.poll(async () => (await api(token, `/networks/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
