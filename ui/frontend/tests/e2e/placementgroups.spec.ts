import type { Locator, Page } from "@playwright/test";
import { expect, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, named, openRow, addLabel, deleteRow, send } from "./console";

type PlacementGroup = { id: number; name: string; type: string; labels: Record<string, string> };

test("UI-02 placement groups", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-pg";
  await openConsole(page, shared.name);
  await section(page, "Placement Groups");
  await helpOpens(page);

  // The form creates a spread placement group.
  await page.getByRole("button", { name: "Create placement group", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: /^Create( placement group)?$/ }).last().click();
  await expect
    .poll(async () => (await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name)).length, { timeout: 60_000 })
    .toBe(1);
  const [created] = await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name);
  expect(created.type).toBe("spread");
  await section(page, "Placement Groups");

  // A label added in its Labels tab is in the API.
  await openRow(page, name);
  await addLabel(page);
  await expect
    .poll(async () => (await named<PlacementGroup>(token, "/placement_groups", "placement_groups", name))[0]?.labels, { timeout: 30_000 })
    .toEqual({ env: "e2e" });

  // Delete after the confirmation removes it from the list and the API.
  await section(page, "Placement Groups");
  await deleteRow(page, name);
  await expect.poll(async () => (await api(token, `/placement_groups/${created.id}`)).status, { timeout: 60_000 }).toBe(404);
});

/**
 * bridgeWrites wraps the page's fetch, through which the Wails runtime sends
 * every call of a bound Go method (E9) as a POST to /wails/runtime, and
 * records the management API calls that create or change a placement group.
 * The returned stop restores fetch and returns what was recorded. The
 * console sends such a call synchronously in its submit handler, so it is
 * recorded before the click that submits returns.
 */
async function bridgeWrites(page: Page): Promise<() => Promise<string[]>> {
  await page.evaluate(() => {
    const w = window as unknown as { __ui02Fetch?: typeof fetch; __ui02Writes?: string[] };
    w.__ui02Writes = [];
    w.__ui02Fetch = window.fetch;
    const original = window.fetch;
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (url.includes("/wails/runtime") && typeof init?.body === "string") {
        try {
          // The runtime sends {object, method, args: {"call-id", methodName, args}};
          // Service.Call's own arguments are method, path and body.
          const call = JSON.parse(init.body) as { args?: { args?: unknown[] } };
          const [method, path] = call.args?.args ?? [];
          if ((method === "POST" || method === "PUT") && typeof path === "string" && path.includes("/placement_groups")) {
            w.__ui02Writes!.push(`${method} ${path}`);
          }
        } catch {
          // not a JSON call; not one of ours
        }
      }
      return original(input, init);
    };
  });
  return () =>
    page.evaluate(() => {
      const w = window as unknown as { __ui02Fetch?: typeof fetch; __ui02Writes?: string[] };
      if (w.__ui02Fetch) window.fetch = w.__ui02Fetch;
      return w.__ui02Writes ?? [];
    });
}

/**
 * refusedSubmit leaves a text breaking the label rule in the labels box,
 * moves on and presses submit and checks that the form sends nothing and keeps the
 * box's error line visible.
 */
async function refusedSubmit(page: Page, submit: Locator): Promise<void> {
  const box = page.getByRole("textbox", { name: "Labels", exact: true });
  await box.fill("-bad-");
  // Leaving the box shows the error line first, so the line does not move
  // the submit button away between mouse down and up.
  await box.blur();
  const error = page.getByText(/Label key "-bad-" is invalid/);
  await expect(error).toBeVisible({ timeout: 10_000 });
  const stop = await bridgeWrites(page);
  await submit.click();
  expect(await stop(), "bridge calls that write a placement group").toEqual([]);
  await expect(box).toHaveAccessibleDescription(/Label key "-bad-" is invalid/, { timeout: 10_000 });
  await expect(submit).toBeVisible();
  await expect(box).toHaveValue("-bad-");
  await expect(error).toBeVisible();
}

test("UI-02 invalid labels", async ({ page, shared }) => {
  test.setTimeout(5 * 60_000);
  const token = shared.token;
  await openConsole(page, shared.name);

  // The create form refuses the submit.
  const formName = "ui02-pg-bad-labels";
  await section(page, "Placement Groups");
  await page.getByRole("button", { name: "Create placement group", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill(formName);
  await refusedSubmit(page, page.getByRole("button", { name: /^Create( placement group)?$/ }).last());
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(formName);
  expect(await named<PlacementGroup>(token, "/placement_groups", "placement_groups", formName)).toEqual([]);

  // "Save labels" on the Labels tab refuses the submit.
  const tabName = "ui02-pg-labels-tab";
  const made = await send(token, "POST", "/placement_groups", { name: tabName, type: "spread", labels: { keep: "yes" } });
  expect(made.status, "POST /placement_groups").toBe(201);
  const id = (made.body as { placement_group: PlacementGroup }).placement_group.id;
  try {
    await section(page, "Placement Groups");
    await openRow(page, tabName);
    await page.getByRole("link", { name: "Labels", exact: true }).click();
    await refusedSubmit(page, page.getByRole("button", { name: "Save labels", exact: true }));
    const [after] = await named<PlacementGroup>(token, "/placement_groups", "placement_groups", tabName);
    expect(after?.labels).toEqual({ keep: "yes" });
  } finally {
    const del = await send(token, "DELETE", `/placement_groups/${id}`);
    expect(del.status, `DELETE /placement_groups/${id}`).toBe(204);
  }
});
