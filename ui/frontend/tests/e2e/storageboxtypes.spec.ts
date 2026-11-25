import type { Locator } from "@playwright/test";
import { test, expect, env, openView, rowAction, tableRow, uniqueName, confirmTyped } from "./fixtures";
import { helpOpens } from "./console";

/** The Hetzner API of hlab (storage boxes and their types). */
const hetznerEndpoint = "http://api.hetzner.internal/hetzner/v1";

type BoxType = {
  id: number;
  name: string;
  description: string;
  size: number;
  snapshot_limit: number;
  automatic_snapshot_limit: number;
  subaccounts_limit: number;
  prices: { location: string }[];
};

/** served is the type name of `/storage_box_types` of the run's project, or undefined. */
async function served(name: string): Promise<BoxType | undefined> {
  const res = await fetch(`${hetznerEndpoint}/storage_box_types?per_page=50`, {
    headers: { Authorization: `Bearer ${env("HLAB_E2E_API_TOKEN")}` },
  });
  if (res.status !== 200) throw new Error(`GET /storage_box_types: status ${res.status}`);
  const body = (await res.json()) as { storage_box_types: BoxType[] };
  return body.storage_box_types.find((t) => t.name === name);
}

/** locationOf is the location name of a checkbox labelled "<name> (<city>)". */
async function locationOf(box: Locator): Promise<string> {
  const label =
    (await box.getAttribute("aria-label")) ?? (await box.evaluate((el) => (el as HTMLInputElement).labels?.[0]?.textContent ?? ""));
  const name = /^\s*(\S+) \(/.exec(label)?.[1];
  expect(name, `location name in "${label}"`).toBeTruthy();
  return name!;
}

test("UI-14", async ({ page }) => {
  test.setTimeout(4 * 60_000);
  // Storage Box Types is an Administration view like Server Types.
  await openView(page, "Storage Box Types");

  // The catalog is listed with its source, and the view has its help text.
  const shipped = tableRow(page, "bx11");
  await expect(shipped).toBeVisible();
  await expect(shipped.getByRole("cell", { name: "catalog", exact: true })).toBeVisible();
  await helpOpens(page);

  // Add an own type of 64 MiB with its limits and one location unchecked.
  const name = uniqueName("ui14");
  await page.getByRole("button", { name: "Add storage box type", exact: true }).click();
  const form = page.getByRole("dialog");
  await form.getByLabel("Name", { exact: true }).fill(name);
  await form.getByLabel("Description", { exact: true }).fill("e2e own box type");
  await form.getByLabel("Size", { exact: true }).fill("64");
  await form.getByLabel("Unit", { exact: true }).selectOption("MiB");
  await form.getByLabel("Snapshot limit", { exact: true }).fill("7");
  await form.getByLabel("Automatic snapshot limit", { exact: true }).fill("3");
  await form.getByLabel("Subaccounts limit", { exact: true }).fill("20");

  // An "All locations" box, where the view has one, is unchecked first so
  // the single locations can be chosen.
  const all = form.getByRole("checkbox", { name: "All locations", exact: true });
  if ((await all.count()) > 0 && (await all.isChecked())) {
    await all.uncheck();
  }
  const locations = form.getByRole("checkbox", { name: /^\S+ \(.+\)$/ });
  await expect(locations.first()).toBeVisible();
  const boxes = await locations.all();
  expect(boxes.length, "the catalog has at least two locations").toBeGreaterThan(1);
  for (const box of boxes) {
    if (!(await box.isChecked())) await box.check();
  }
  const left = boxes[boxes.length - 1];
  const dropped = await locationOf(left);
  await left.uncheck();
  const kept: string[] = [];
  for (const box of boxes.slice(0, -1)) kept.push(await locationOf(box));

  await form.getByRole("button", { name: "Add", exact: true }).click();
  await expect(form).toBeHidden();
  const own = tableRow(page, name);
  await expect(own).toBeVisible();
  await expect(own.getByRole("cell", { name: "own", exact: true })).toBeVisible();

  // A project serves it with the size in bytes, the limits and only the checked locations.
  await expect
    .poll(
      async () => {
        const t = await served(name);
        return (
          t && {
            size: t.size,
            snapshot_limit: t.snapshot_limit,
            automatic_snapshot_limit: t.automatic_snapshot_limit,
            subaccounts_limit: t.subaccounts_limit,
            locations: t.prices.map((p) => p.location).sort(),
          }
        );
      },
      { timeout: 30_000 },
    )
    .toEqual({ size: 67108864, snapshot_limit: 7, automatic_snapshot_limit: 3, subaccounts_limit: 20, locations: [...kept].sort() });
  expect(kept).not.toContain(dropped);

  // Edit changes the description; the name is read-only.
  await rowAction(own, "Edit");
  const edit = page.getByRole("dialog");
  await expect(edit.getByLabel("Name", { exact: true })).toHaveValue(name);
  await expect(edit.getByLabel("Name", { exact: true })).not.toBeEditable();
  await edit.getByLabel("Description", { exact: true }).fill("e2e own box type, edited");
  await edit.getByRole("button", { name: "Save", exact: true }).click();
  await expect(edit).toBeHidden();
  await expect.poll(async () => (await served(name))?.description, { timeout: 30_000 }).toBe("e2e own box type, edited");

  // Remove after the confirmation takes it out of the view and the API.
  await rowAction(own, "Remove");
  await confirmTyped(page, "Remove", name);
  await expect(own).toBeHidden();
  await expect.poll(async () => (await served(name)) === undefined, { timeout: 30_000 }).toBe(true);
});
