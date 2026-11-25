import type { Page } from "@playwright/test";
import { test, expect, env, tableRow, api, confirmTyped } from "./fixtures";
import { newProject, openConsole, section, helpOpens, menuAction } from "./console";

type Server = { id: number; name: string; status: string };

async function serversNamed(token: string, name: string): Promise<Server[]> {
  const r = await api(token, `/servers?name=${encodeURIComponent(name)}`);
  if (r.status !== 200) throw new Error(`GET /servers: status ${r.status}`);
  return (r.body as { servers: Server[] }).servers;
}

async function serverStatus(token: string, id: number): Promise<string> {
  const r = await api(token, `/servers/${id}`);
  return r.status === 200 ? (r.body as { server: Server }).server.status : `status ${r.status}`;
}

/**
 * pick chooses the entry named name in the form control labelled label: a
 * select (option value or text) or a group of radio buttons.
 */
async function pick(page: Page, label: string, name: string): Promise<void> {
  // Wait for the control first: count() does not wait, and the form renders
  // after the click that opens it.
  await expect(page.getByRole("combobox", { name: label, exact: true }).or(page.getByRole("radiogroup", { name: label, exact: true }))).toBeVisible({ timeout: 30_000 });
  const select = page.getByRole("combobox", { name: label, exact: true });
  if ((await select.count()) > 0) {
    await expect(select.locator("option").filter({ hasText: name }).first()).toBeAttached();
    const value = await select.evaluate((el, n) => {
      const opts = Array.from((el as HTMLSelectElement).options);
      const o = opts.find((x) => x.value === n) ?? opts.find((x) => x.text.split(/[\s()]+/).includes(n)) ?? opts.find((x) => x.text.includes(n));
      return o?.value;
    }, name);
    expect(value, `${label} offers ${name}`).toBeDefined();
    await select.selectOption(value!);
    return;
  }
  const group = page.getByRole("radiogroup", { name: label, exact: true });
  await group.getByRole("radio", { name: new RegExp(`\\b${name.replace(/[.]/g, "\\.")}\\b`) }).first().check();
}

/** watchToasts records every text the toast region shows from now on. */
async function watchToasts(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as unknown as { __toasts: string[] };
    w.__toasts = [];
    const record = () => {
      for (const el of Array.from(document.querySelectorAll("[aria-live]"))) {
        const t = (el.textContent ?? "").trim();
        if (t) w.__toasts.push(t);
      }
    };
    new MutationObserver(record).observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
  });
}

function toastTexts(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __toasts: string[] }).__toasts);
}

test("UI-02 servers", async ({ page }) => {
  // The first server boot may download and convert the image.
  test.setTimeout(35 * 60_000);
  const project = await newProject("e2e-ui02-srv");
  try {
    // The console of a new project opens on Servers; its help opens.
    await openConsole(page, project.name);
    await expect(page.getByRole("heading", { level: 1, name: "Servers", exact: true })).toBeVisible();
    await helpOpens(page);

    // Create a server through the form, without an SSH key.
    const name = "ui02-srv";
    await page.getByRole("button", { name: "Create server", exact: true }).click();
    await pick(page, "Location", env("HLAB_E2E_SERVER_LOCATION"));
    await pick(page, "Image", env("HLAB_E2E_SERVER_IMAGE"));
    await pick(page, "Type", env("HLAB_E2E_SERVER_TYPE"));
    await page.getByLabel("Name", { exact: true }).fill(name);
    await page.getByRole("button", { name: /^Create( server)?$/ }).last().click();

    // The root password shows once in its dialog.
    const secret = page.getByRole("dialog");
    await expect(secret.getByText(/not shown again/i)).toBeVisible({ timeout: 5 * 60_000 });
    const password = ((await secret.locator("code").first().textContent()) ?? "").trim();
    expect(password.length, "the dialog shows the root password").toBeGreaterThan(0);
    await secret.getByRole("button", { name: "Close", exact: true }).click();
    await expect(secret).toBeHidden();
    await expect(page.getByText(password, { exact: true })).toHaveCount(0);

    await expect.poll(async () => (await serversNamed(project.token, name)).length, { timeout: 60_000 }).toBe(1);
    const [{ id }] = await serversNamed(project.token, name);
    await expect.poll(() => serverStatus(project.token, id), { timeout: 30 * 60_000, intervals: [5_000] }).toBe("running");
    await section(page, "Servers");
    const row = tableRow(page, name);
    await expect(row.getByText(/^running$/i)).toBeVisible({ timeout: 30_000 });

    // Rename on the detail page changes the name in the API.
    await row.getByRole("cell", { name, exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();
    const renamed = "ui02-srv-renamed";
    await menuAction(page, "Rename");
    let dialog = page.getByRole("dialog");
    await dialog.getByLabel("Name", { exact: true }).fill(renamed);
    await dialog.getByRole("button", { name: /^(Rename|Save)$/ }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(async () => (await serversNamed(project.token, renamed)).map((s) => s.id), { timeout: 30_000 }).toEqual([id]);
    await expect(page.getByRole("heading", { level: 1, name: renamed, exact: true })).toBeVisible({ timeout: 30_000 });

    // Power off asks for confirmation; a toast shows the running action with
    // its progress and ends in success; the API reports "off".
    await watchToasts(page);
    await menuAction(page, "Power off");
    dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "Power off", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(() => serverStatus(project.token, id), { timeout: 5 * 60_000 }).toBe("off");
    await expect.poll(async () => (await toastTexts(page)).some((t) => /\d+\s*%/.test(t)), { timeout: 30_000 }).toBe(true);
    // A successful action toast closes by itself; an error toast would stay.
    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect.poll(async () => ((await page.locator("[aria-live]").first().textContent()) ?? "").trim(), { timeout: 60_000 }).toBe("");

    // Delete needs the name typed and removes the server from the list and the API.
    await menuAction(page, "Delete");
    await confirmTyped(page, "Delete", renamed);
    await expect(page.getByRole("heading", { level: 1, name: "Servers", exact: true })).toBeVisible({ timeout: 2 * 60_000 });
    await expect(tableRow(page, renamed)).toBeHidden({ timeout: 2 * 60_000 });
    await expect.poll(() => serverStatus(project.token, id), { timeout: 5 * 60_000 }).toBe("status 404");
  } finally {
    await project.remove();
  }
});
