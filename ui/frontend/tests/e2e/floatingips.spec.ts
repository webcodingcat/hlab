import { expect, env, api, tableRow } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, named, openRow, addLabel, deleteRow } from "./console";

type FloatingIp = { id: number; name: string; ip: string; type: string; home_location: { name: string }; labels: Record<string, string> };

test("UI-02 floating IPs", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-fip";
  const location = env("HLAB_E2E_SERVER_LOCATION");
  await openConsole(page, shared.name);
  await section(page, "Floating IPs");
  await helpOpens(page);

  // The form creates an IPv4 floating IP with Location and Name.
  await page.getByRole("button", { name: "Create floating IP", exact: true }).click();
  await pick(page, "Location", location);
  await pick(page, "Protocol", "IPv4");
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: /^Create( floating IP)?$/ }).last().click();
  await expect.poll(async () => (await named<FloatingIp>(token, "/floating_ips", "floating_ips", name)).length, { timeout: 60_000 }).toBe(1);
  const [created] = await named<FloatingIp>(token, "/floating_ips", "floating_ips", name);
  expect(created.type).toBe("ipv4");
  expect(created.home_location.name).toBe(location);

  // It is listed with its address.
  await section(page, "Floating IPs");
  const row = tableRow(page, name);
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText(created.ip, { exact: true })).toBeVisible();

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect
    .poll(async () => (await named<FloatingIp>(token, "/floating_ips", "floating_ips", name))[0]?.labels, { timeout: 30_000 })
    .toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Floating IPs");
  await deleteRow(page, name, false);
  await expect.poll(async () => (await api(token, `/floating_ips/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
