import { expect, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, named, openRow, addLabel, deleteRow } from "./console";

type Rule = { direction: string; protocol: string; port?: string | null; source_ips: string[]; destination_ips: string[] };
type Firewall = { id: number; name: string; rules: Rule[]; labels: Record<string, string> };

test("UI-02 firewalls", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-fw";
  await openConsole(page, shared.name);
  await section(page, "Firewalls");
  await helpOpens(page);

  // The form creates a firewall with one inbound rule (TCP 22 from anywhere) and a Name.
  await page.getByRole("button", { name: "Create firewall", exact: true }).click();
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  const protocol = page.getByRole("combobox", { name: "Protocol", exact: true });
  if ((await protocol.count()) === 0) {
    // The inbound table comes first.
    await page.getByRole("button", { name: /^Add (inbound )?rule$/ }).first().click();
  }
  await protocol.first().selectOption("tcp");
  await page.getByLabel("Port", { exact: true }).first().fill("22");
  await page.getByLabel(/^(Source )?IPs$/).first().fill("0.0.0.0/0");
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: /^Create( firewall)?$/ }).last().click();
  await expect.poll(async () => (await named<Firewall>(token, "/firewalls", "firewalls", name)).length, { timeout: 60_000 }).toBe(1);
  const [created] = await named<Firewall>(token, "/firewalls", "firewalls", name);
  expect(created.rules).toHaveLength(1);
  expect(created.rules[0]).toMatchObject({ direction: "in", protocol: "tcp", port: "22", source_ips: ["0.0.0.0/0"] });
  await section(page, "Firewalls");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect.poll(async () => (await named<Firewall>(token, "/firewalls", "firewalls", name))[0]?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Firewalls");
  await deleteRow(page, name);
  await expect.poll(async () => (await api(token, `/firewalls/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});
