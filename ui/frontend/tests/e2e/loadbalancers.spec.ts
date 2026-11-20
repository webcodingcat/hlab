import { expect, env, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, named, openRow, addLabel, deleteRow } from "./console";

type LoadBalancer = { id: number; name: string; load_balancer_type: { name: string }; location: { name: string }; labels: Record<string, string> };

test("UI-02 load balancers", async ({ page, shared }) => {
  test.setTimeout(15 * 60_000);
  const token = shared.token;
  const name = "ui02-lb";
  const location = env("HLAB_E2E_SERVER_LOCATION");
  const types = await api(token, "/load_balancer_types");
  expect(types.status).toBe(200);
  const type = (types.body as { load_balancer_types: { name: string }[] }).load_balancer_types[0]?.name;
  expect(type, "the API offers a load balancer type").toBeTruthy();

  await openConsole(page, shared.name);
  await section(page, "Load Balancers");
  await helpOpens(page);

  // The form creates a load balancer with Location, Type and Name.
  await page.getByRole("button", { name: "Create load balancer", exact: true }).click();
  await pick(page, "Location", location);
  await pick(page, "Type", type!);
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: /^Create( load balancer)?$/ }).last().click();
  await expect
    .poll(async () => (await named<LoadBalancer>(token, "/load_balancers", "load_balancers", name)).length, { timeout: 5 * 60_000 })
    .toBe(1);
  const [created] = await named<LoadBalancer>(token, "/load_balancers", "load_balancers", name);
  expect(created.location.name).toBe(location);
  expect(created.load_balancer_type.name).toBe(type);
  // Its create action locks it until it ends (API-13); Delete waits for that.
  await expect
    .poll(
      async () =>
        ((await api(token, `/load_balancers/${created.id}/actions?status=running`)).body as { actions: unknown[] }).actions.length,
      { timeout: 2 * 60_000 },
    )
    .toBe(0);
  await section(page, "Load Balancers");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect
    .poll(async () => (await named<LoadBalancer>(token, "/load_balancers", "load_balancers", name))[0]?.labels, { timeout: 30_000 })
    .toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Load Balancers");
  await deleteRow(page, name, false);
  await expect.poll(async () => (await api(token, `/load_balancers/${created.id}`)).status, { timeout: 2 * 60_000 }).toBe(404);
});
