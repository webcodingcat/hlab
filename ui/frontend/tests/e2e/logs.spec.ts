import { test, expect, openView, hlab, uniqueName } from "./fixtures";

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
