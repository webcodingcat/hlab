// Helpers of the project console tests (UI-02): a project of their own
// with a token, made and removed through hlab, requests to its API with
// that token, and the way into its console through the project list; the
// shared project of the phase 14 sections and the steps they have in common.
import type { Page } from "@playwright/test";
import { test, expect, hlab, openView, tableRow, rowAction, uniqueName, apiEndpoint, api } from "./fixtures";

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

/**
 * resourceTest is the test of the phase 14 sections: they share one project
 * e2e-ui02-res-<time> with a read/write token, made through hlab when the
 * first of them needs it and deleted when the worker ends. Each test deletes
 * what it made.
 */
export const resourceTest = test.extend<{}, { shared: TestProject }>({
  shared: [
    async ({}, use) => {
      const project = await newProject("e2e-ui02-res");
      try {
        await use(project);
      } finally {
        await project.remove();
      }
    },
    { scope: "worker", timeout: 5 * 60_000 },
  ],
});

/** named lists the resources of path whose name is name, from the array key of the answer. */
export async function named<T>(token: string, path: string, key: string, name: string): Promise<T[]> {
  const r = await api(token, `${path}?name=${encodeURIComponent(name)}`);
  if (r.status !== 200) throw new Error(`GET ${path}: status ${r.status}`);
  return (r.body as Record<string, T[]>)[key];
}

/**
 * pick chooses the entry name in the form control labelled label: a select
 * (option value or text, case-insensitive) or a group of radio buttons.
 */
export async function pick(page: Page, label: string, name: string): Promise<void> {
  const select = page.getByRole("combobox", { name: label, exact: true });
  const group = page.getByRole("radiogroup", { name: label, exact: true });
  // count() does not wait; the form renders after the click that opens it.
  await expect(select.or(group).first()).toBeVisible({ timeout: 30_000 });
  if ((await select.count()) > 0) {
    await expect.poll(() => select.first().locator("option").count(), { timeout: 30_000 }).toBeGreaterThan(1);
    const value = await select.first().evaluate((el, n) => {
      const want = n.toLowerCase();
      const opts = Array.from((el as HTMLSelectElement).options);
      const o =
        opts.find((x) => x.value.toLowerCase() === want) ??
        opts.find((x) => x.text.toLowerCase().split(/[\s()]+/).includes(want)) ??
        opts.find((x) => x.text.toLowerCase().includes(want));
      return o?.value;
    }, name);
    expect(value, `${label} offers ${name}`).toBeDefined();
    await select.first().selectOption(value!);
    return;
  }
  await group.getByRole("radio", { name: new RegExp(`\b${name.replace(/[.]/g, "\.")}\b`, "i") }).first().check();
}

/** openRow opens the detail page of the list row named name. */
export async function openRow(page: Page, name: string): Promise<void> {
  const row = tableRow(page, name);
  await expect(row).toBeVisible({ timeout: 30_000 });
  await row.getByRole("cell", { name, exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();
}

/** addLabel adds the label env=e2e in the detail page's Labels tab and saves it. */
export async function addLabel(page: Page): Promise<void> {
  await page.getByRole("link", { name: "Labels", exact: true }).click();
  await page.getByRole("button", { name: "Add label", exact: true }).click();
  await page.getByRole("textbox", { name: /key/i }).last().fill("env");
  await page.getByRole("textbox", { name: /value/i }).last().fill("e2e");
  await page.getByRole("button", { name: "Save labels", exact: true }).click();
}

/**
 * deleteRow deletes the list row named name through its row menu; with
 * typed the dialog's Delete stays disabled until the name is typed.
 */
export async function deleteRow(page: Page, name: string, typed: boolean): Promise<void> {
  const row = tableRow(page, name);
  await expect(row).toBeVisible({ timeout: 30_000 });
  await rowAction(row, "Delete");
  const dialog = page.getByRole("dialog");
  const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
  if (typed) {
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill(name);
  }
  await confirm.click();
  await expect(row).toBeHidden({ timeout: 2 * 60_000 });
}
