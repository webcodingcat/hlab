import { test, expect, openView, hlab, uniqueName } from "./fixtures";
import type { Locator } from "@playwright/test";

test("UI-06", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Logs");

  // The service log is shown.
  const log = page.getByRole("log");
  await expect(log).toBeVisible();
  await expect(log).not.toBeEmpty();

  // While following, a line the service writes later appears.
  await page.getByLabel("Follow").check();
  const project = uniqueName("ui06");
  const created = await hlab("project", "create", project);
  expect(created.code, `hlab project create: ${created.stderr}`).toBe(0);
  // hlabd logs the deletion of a project with its name.
  const deleted = await hlab("project", "delete", project);
  expect(deleted.code, `hlab project delete: ${deleted.stderr}`).toBe(0);
  await expect(log.getByText(project)).not.toHaveCount(0, { timeout: 60_000 });
});

/** shownLines are the lines the log list shows, top to bottom. */
async function shownLines(log: Locator): Promise<string[]> {
  return (await log.innerText()).split(/\r?\n/).filter((l) => l.trim() !== "");
}

test("UI-06 search", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Logs");
  const log = page.getByRole("log");
  const follow = page.getByLabel("Follow");
  await follow.check();

  // The service logs a created project's name.
  const project = uniqueName("ui06s");
  const created = await hlab("project", "create", project);
  expect(created.code, `hlab project create: ${created.stderr}`).toBe(0);
  const deleted = await hlab("project", "delete", project);
  expect(deleted.code, `hlab project delete: ${deleted.stderr}`).toBe(0);
  await expect(log.getByText(project)).not.toHaveCount(0, { timeout: 60_000 });

  // The search shows the matches highlighted with their count; follow is off.
  const search = page.getByRole("searchbox");
  await search.fill(project);
  await search.press("Enter");
  const marks = log.locator("mark");
  await expect(marks).not.toHaveCount(0, { timeout: 60_000 });
  for (const m of await marks.allInnerTexts()) expect(m.toLowerCase()).toBe(project);
  const count = page.getByText(/^\d+\+? match(es)?$/);
  await expect(count).toBeVisible();
  expect(Number(/^\d+/.exec((await count.innerText()).trim())![0])).toBeGreaterThan(0);
  await expect(follow).not.toBeChecked();

  // Clearing the field returns to the live view.
  await search.fill("");
  await expect(marks).toHaveCount(0);
  await expect(count).toBeHidden();
  await expect(follow).toBeChecked();
  await expect(log).not.toBeEmpty();
});

test("UI-06 older", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Logs");
  const log = page.getByRole("log");
  await expect(log).not.toBeEmpty();
  const older = page.getByRole("button", { name: "Load older lines" });
  await expect(older).toBeVisible();

  if (await older.isDisabled()) {
    // The log starts within the shown lines.
    test.info().annotations.push({ type: "UI-06 older", description: "the log starts within the shown lines" });
    return;
  }

  // The lines before the oldest shown one are put in front of it.
  const before = await shownLines(log);
  await older.click();
  await expect.poll(async () => (await shownLines(log)).length, { timeout: 60_000 }).toBeGreaterThan(before.length);
  const after = await shownLines(log);
  expect(after.indexOf(before[0]), "the formerly oldest line moved down").toBeGreaterThan(0);
});
