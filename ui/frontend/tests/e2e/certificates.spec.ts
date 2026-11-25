import { expect, api, uniqueName } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, send, named, openRow, addLabel, deleteRow } from "./console";

type Certificate = { id: number; name: string; type: string; domain_names: string[]; labels: Record<string, string> };

test("UI-02 certificates", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-cert";
  const zoneName = `${uniqueName("ui02-cert")}.internal`;
  const zone = await send(token, "POST", "/zones", { name: zoneName, mode: "primary" });
  expect(zone.status, `POST /zones: ${JSON.stringify(zone.body)}`).toBe(201);
  const zoneId = (zone.body as { zone: { id: number } }).zone.id;
  const domain = `www.${zoneName}`;
  try {
    await openConsole(page, shared.name);
    await section(page, "Certificates");
    await helpOpens(page);

    // The form creates a managed certificate for a name in the zone.
    await page.getByRole("button", { name: "Add certificate", exact: true }).click();
    await pick(page, "Type", "managed");
    await page.getByLabel("Name", { exact: true }).fill(name);
    await page.getByLabel("Domain names", { exact: true }).fill(domain);
    await page.getByRole("button", { name: /^(Add|Create)( certificate)?$/ }).last().click();
    await expect.poll(async () => (await named<Certificate>(token, "/certificates", "certificates", name)).length, { timeout: 60_000 }).toBe(1);
    const [created] = await named<Certificate>(token, "/certificates", "certificates", name);
    expect(created.type).toBe("managed");
    expect(created.domain_names).toEqual([domain]);
    await section(page, "Certificates");

    // A label added in its Labels tab is in the API.
    await openRow(page, name);
    await addLabel(page);
    await expect
      .poll(async () => (await named<Certificate>(token, "/certificates", "certificates", name))[0]?.labels, { timeout: 30_000 })
      .toEqual({ env: "e2e" });

    // Delete after the confirmation removes it from the list and the API.
    await section(page, "Certificates");
    await deleteRow(page, name);
    await expect.poll(async () => (await api(token, `/certificates/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
  } finally {
    await send(token, "DELETE", `/zones/${zoneId}`);
  }
});
