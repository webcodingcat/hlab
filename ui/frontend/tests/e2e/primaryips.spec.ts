import { expect, env, api, tableRow } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, named, openRow, addLabel, deleteRow } from "./console";

type PrimaryIp = { id: number; name: string; ip: string; type: string; assignee_id: number | null; location: { name: string }; labels: Record<string, string> };

test("UI-02 primary IPs", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-pip";
  const location = env("HLAB_E2E_SERVER_LOCATION");
  await openConsole(page, shared.name);
  await section(page, "Primary IPs");
  await helpOpens(page);

  // The form creates an unassigned IPv4 primary IP with Name and Location.
  await page.getByRole("button", { name: "Create primary IP", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await pick(page, "Location", location);
  await pick(page, "Protocol", "IPv4");
  await page.getByRole("button", { name: /^Create( primary IP)?$/ }).last().click();
  await expect.poll(async () => (await named<PrimaryIp>(token, "/primary_ips", "primary_ips", name)).length, { timeout: 60_000 }).toBe(1);
  const [created] = await named<PrimaryIp>(token, "/primary_ips", "primary_ips", name);
  expect(created.type).toBe("ipv4");
  expect(created.assignee_id).toBeNull();
  expect(created.location.name).toBe(location);

  // It is listed with its address.
  await section(page, "Primary IPs");
  const row = tableRow(page, name);
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText(created.ip, { exact: true })).toBeVisible();

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect
    .poll(async () => (await named<PrimaryIp>(token, "/primary_ips", "primary_ips", name))[0]?.labels, { timeout: 30_000 })
    .toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Primary IPs");
  await deleteRow(page, name, false);
  await expect.poll(async () => (await api(token, `/primary_ips/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
