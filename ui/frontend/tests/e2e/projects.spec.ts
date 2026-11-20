import { test, expect, openView, rowAction, tableRow, api, uniqueName } from "./fixtures";

test("UI-05", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Projects");

  // Create a project.
  const project = uniqueName("ui05");
  await page.getByRole("button", { name: "Create project" }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill(project);
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  await expect(dialog).toBeHidden();
  const row = tableRow(page, project);
  await expect(row).toBeVisible();

  // A row click opens the project console of that project with "API tokens",
  // which its sidebar opens.
  await row.getByRole("cell", { name: project, exact: true }).click();
  await expect(page.getByRole("banner").getByText(project).first()).toBeVisible();
  const tokensLink = page.getByRole("navigation").getByRole("link", { name: "API tokens", exact: true });
  await expect(tokensLink).toBeVisible();
  await tokensLink.click();
  await expect(page.getByRole("heading", { level: 1, name: "API tokens", exact: true })).toBeVisible();

  // Create a token; its value is shown once.
  await page.getByRole("button", { name: "Create token" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("ui05");
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  const value = dialog.getByText(/^[A-Za-z0-9]{64}$/);
  await expect(value).toBeVisible();
  const token = (await value.textContent())!.trim();
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(token)).toHaveCount(0);

  // The token reads the project's API.
  expect((await api(token, "/servers")).status).toBe(200);

  // Revoke it; the API then refuses it.
  const tokenRow = tableRow(page, "ui05");
  await expect(tokenRow).toBeVisible();
  await rowAction(tokenRow, "Revoke");
  await page.getByRole("dialog").getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(tokenRow).toBeHidden();
  await expect.poll(async () => (await api(token, "/servers")).status, { timeout: 30_000 }).toBe(401);

  // The gear opens Administration and, clicked again, returns to this
  // project console.
  const gear = page.getByRole("banner").getByRole("link", { name: "Administration", exact: true });
  await gear.click();
  await expect(gear).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("banner").getByText("Administration", { exact: true })).toBeVisible();
  await gear.click();
  await expect(gear).not.toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "API tokens", exact: true })).toBeVisible();
  await expect(page.getByRole("banner").getByText(project).first()).toBeVisible();

  // Back in the list through the breadcrumb, delete the project after its
  // name is typed.
  await page.getByRole("banner").getByRole("link", { name: "Projects", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Projects", exact: true })).toBeVisible();
  await rowAction(tableRow(page, project), "Delete");
  dialog = page.getByRole("dialog");
  const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("textbox").fill(project);
  await confirm.click();
  await expect(tableRow(page, project)).toBeHidden({ timeout: 2 * 60_000 });
});
