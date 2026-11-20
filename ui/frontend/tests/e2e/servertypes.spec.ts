import { test, expect, env, openView, rowAction, tableRow, api, uniqueName } from "./fixtures";

type TypeList = { server_types?: { name: string; locations?: { name: string }[] }[] };

/**
 * served is what `/server_types?name=` of the run's project gives: each
 * type's name with its location names.
 */
async function served(name: string): Promise<string[]> {
  const r = await api(env("HLAB_E2E_API_TOKEN"), `/server_types?name=${encodeURIComponent(name)}`);
  if (r.status !== 200) return [`status ${r.status}`];
  return ((r.body as TypeList).server_types ?? []).map((t) => `${t.name} @ ${(t.locations ?? []).map((l) => l.name).join(",")}`);
}

test("UI-04", async ({ page }) => {
  test.setTimeout(10 * 60_000);
  await openView(page, "Server Types");

  // The catalog is listed with its source.
  const shipped = tableRow(page, "cx23");
  await expect(shipped).toBeVisible();
  await expect(shipped.getByRole("cell", { name: "catalog", exact: true })).toBeVisible();

  // Add an own type.
  const name = uniqueName("ui04");
  await page.getByRole("button", { name: "Add server type" }).click();
  const form = page.getByRole("dialog");
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Description").fill("e2e own type");
  await form.getByLabel("Cores").fill("2");
  await form.getByLabel("Memory (GB)").fill("2");
  await form.getByLabel("Disk (GB)").fill("20");
  await form.getByLabel("CPU type").selectOption("shared");

  // "All locations" unchecked and no location: the error shows under the list.
  const all = form.getByRole("checkbox", { name: "All locations", exact: true });
  await expect(all).toBeChecked();
  await all.uncheck();
  const locations = form.getByRole("checkbox", { name: /^\S+ \(.+\)$/ });
  await expect(locations.first()).toBeVisible();
  for (const box of await locations.all()) await expect(box).not.toBeChecked();
  await form.getByRole("button", { name: "Add", exact: true }).click();
  const error = form.getByText(/at least one location/i);
  await expect(error).toBeVisible();
  await expect(form).toBeVisible();
  const lastBox = await locations.last().boundingBox();
  const errorBox = await error.boundingBox();
  expect(errorBox!.y, "the error is under the location list").toBeGreaterThan(lastBox!.y);

  // One location ticked: the type is added and served with that location.
  const first = locations.first();
  const label = (await first.getAttribute("aria-label")) ?? (await first.evaluate((el) => (el as HTMLInputElement).labels?.[0]?.textContent ?? ""));
  const location = /^\s*(\S+) \(/.exec(label)?.[1];
  expect(location, `location name in "${label}"`).toBeTruthy();
  await first.check();
  await form.getByRole("button", { name: "Add", exact: true }).click();
  await expect(form).toBeHidden();

  const own = tableRow(page, name);
  await expect(own).toBeVisible();
  await expect(own.getByRole("cell", { name: "own", exact: true })).toBeVisible();
  await expect.poll(() => served(name), { timeout: 30_000 }).toEqual([`${name} @ ${location}`]);

  // Remove it again.
  await rowAction(own, "Remove");
  await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
  await expect(own).toBeHidden();
  await expect.poll(() => served(name), { timeout: 30_000 }).toEqual([]);

  // Update from Hetzner opens a dialog with the token field: with the token
  // of a usable hcloud context it closes and the count shows, without one
  // Hetzner's refusal of an invalid token stays in the dialog.
  const token = process.env.HLAB_E2E_HETZNER_TOKEN ?? "";
  const update = page.getByRole("button", { name: "Update from Hetzner" });
  await update.click();
  let dialog = page.getByRole("dialog");
  let field = dialog.getByLabel("Hetzner token");
  await expect(field).toHaveAttribute("type", "password");
  await field.fill(token || "invalid-e2e-token");
  await dialog.getByRole("button", { name: "Update", exact: true }).click();
  if (token) {
    await expect(dialog).toBeHidden({ timeout: 5 * 60_000 });
    await expect(page.getByText(/\d+ server types/i)).toBeVisible();
  } else {
    await expect(dialog.getByText(/unable to authenticate/i)).toBeVisible({ timeout: 2 * 60_000 });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(dialog).toBeHidden();
  }

  // Reopened, the field is empty.
  await update.click();
  dialog = page.getByRole("dialog");
  field = dialog.getByLabel("Hetzner token");
  await expect(field).toHaveValue("");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(dialog).toBeHidden();
});
