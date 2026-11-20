// Helpers of the project console tests (UI-02): a project of their own
// with a token, made and removed through hlab, requests to its API with
// that token, and the way into its console through the project list.
import type { Page } from "@playwright/test";
import { expect, hlab, openView, tableRow, uniqueName, apiEndpoint } from "./fixtures";

export type TestProject = { name: string; token: string; remove: () => Promise<void> };

/** newProject creates a project <prefix>-<time> and a read/write token for it. */
export async function newProject(prefix: string): Promise<TestProject> {
  const name = uniqueName(prefix);
  let r = await hlab("project", "create", name);
  if (r.code !== 0) throw new Error(`hlab project create ${name}: exit ${r.code}: ${r.stderr}`);
  const remove = async () => {
    const d = await hlab("project", "delete", name);
    if (d.code !== 0) throw new Error(`hlab project delete ${name}: exit ${d.code}: ${d.stderr}`);
  };
  r = await hlab("token", "create", "--project", name, "--name", "ui02", "--output", "json");
  let token = "";
  try {
    token = (JSON.parse(r.stdout.trim()) as { token?: string }).token ?? "";
  } catch {
    // no JSON; handled below
  }
  if (r.code !== 0 || !token) {
    await remove();
    // Never show stdout: it may hold the token.
    throw new Error(`hlab token create --project ${name}: exit ${r.code}: ${r.stderr}`);
  }
  return { name, token, remove };
}

/** send sends one request with a JSON body to hlab's public API. */
export async function send(
  token: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<{ status: number; body: unknown }> {
  const res = await fetch(apiEndpoint + path, {
    method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let parsed: unknown = text;
  try {
    parsed = JSON.parse(text);
  } catch {
    // not JSON; keep the text
  }
  return { status: res.status, body: parsed };
}

/** openConsole opens the console of project through a click on its row in the project list. */
export async function openConsole(page: Page, project: string): Promise<void> {
  await openView(page, "Projects");
  const row = tableRow(page, project);
  // The list refreshes every 5 s; the project was made outside the window.
  await expect(row).toBeVisible({ timeout: 30_000 });
  await row.getByRole("cell", { name: project, exact: true }).click();
  await expect(page.getByRole("banner").getByText(project).first()).toBeVisible();
}

/** section opens a section of the project console through its sidebar. */
export async function section(page: Page, name: string): Promise<void> {
  await page.getByRole("navigation").getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();
}

/** helpOpens checks that the view's help button opens its help text, and closes it again. */
export async function helpOpens(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Help", exact: true }).click();
  const panel = page.getByRole("complementary", { name: "Help" });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("heading").first()).toBeVisible();
  await page.getByRole("button", { name: "Close help", exact: true }).click();
  await expect(panel).toBeHidden();
}

/** menuAction picks an entry of the detail page's actions menu. */
export async function menuAction(page: Page, action: string): Promise<void> {
  await page.getByRole("button", { name: "Actions", exact: true }).first().click();
  await page.getByRole("menuitem", { name: action, exact: true }).click();
}
