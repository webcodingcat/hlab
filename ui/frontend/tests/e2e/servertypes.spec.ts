import { test, expect, env, openView, rowAction, tableRow, api, uniqueName } from "./fixtures";

type TypeList = { server_types?: { name: string }[] };

/** served is the list of names `/server_types?name=` of the run's project gives. */
async function served(name: string): Promise<string[]> {
  const r = await api(env("HLAB_E2E_API_TOKEN"), `/server_types?name=${encodeURIComponent(name)}`);
  if (r.status !== 200) return [`status ${r.status}`];
  return ((r.body as TypeList).server_types ?? []).map((t) => t.name);
}

test("UI-04", async ({ page }) => {
  test.setTimeout(10 * 60_000);
  await openView(page, "Server Types");

  // The catalog is listed with its source.
  const shipped = tableRow(page, "cx23");
  await expect(shipped).toBeVisible();
  await expect(shipped.getByRole("cell", { name: "catalog", exact: true })).toBeVisible();

  // Add an own type; the project's API then serves it.
  const name = uniqueName("ui04");
  await page.getByRole("button", { name: "Add server type" }).click();
  const form = page.getByRole("dialog");
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Description").fill("e2e own type");
  await form.getByLabel("Cores").fill("2");
  await form.getByLabel("Memory (GB)").fill("2");
  await form.getByLabel("Disk (GB)").fill("20");
  await form.getByLabel("CPU type").selectOption("shared");
  await form.getByRole("button", { name: "Add", exact: true }).click();
  await expect(form).toBeHidden();

  const own = tableRow(page, name);
  await expect(own).toBeVisible();
  await expect(own.getByRole("cell", { name: "own", exact: true })).toBeVisible();
  await expect.poll(() => served(name), { timeout: 30_000 }).toEqual([name]);

  // Remove it again.
  await rowAction(own, "Remove");
  await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
  await expect(own).toBeHidden();
  await expect.poll(() => served(name), { timeout: 30_000 }).toEqual([]);

  // Update from Hetzner: with the token of a usable hcloud context the
  // count, without one Hetzner's refusal of an invalid token.
  const token = process.env.HLAB_E2E_HETZNER_TOKEN ?? "";
  const field = page.getByLabel("Hetzner token");
  await expect(field).toHaveAttribute("type", "password");
  await field.fill(token || "invalid-e2e-token");
  await page.getByRole("button", { name: "Update from Hetzner" }).click();
  if (token) {
    await expect(page.getByText(/\d+ server types/i)).toBeVisible({ timeout: 5 * 60_000 });
  } else {
    await expect(page.getByText(/unable to authenticate/i)).toBeVisible({ timeout: 2 * 60_000 });
  }
  await expect(field).toHaveValue("");
});
