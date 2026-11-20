// Shared fixtures of the tray UI tests. They run against the real tray
// window of the e2e build (tag e2e), which test/e2e/ui_test.go starts with
// a remote-debugging port; Playwright connects over CDP and drives the
// existing window instead of launching a browser.
import { test as base, expect, chromium } from "@playwright/test";
import type { Browser, Locator, Page } from "@playwright/test";
import { execFile } from "node:child_process";

export { expect };

const appOrigin = "http://wails.localhost";

/** env returns a variable the Go harness sets, failing the test without it. */
export function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; run the UI rows through test/e2e (task e2e -- -run 'TestE2E/UI-')`);
  return v;
}

/** The public API of hlab, as clients reach it. */
export const apiEndpoint = "http://api.hetzner.internal/v1";

type Fixtures = { page: Page };
type WorkerFixtures = { cdp: Browser };

export const test = base.extend<Fixtures, WorkerFixtures>({
  // One CDP connection per worker; the configuration runs one worker.
  cdp: [
    async ({}, use) => {
      const port = env("HLAB_E2E_CDP_PORT");
      const browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
      await use(browser);
      // On a CDP connection this only disconnects; the tray keeps running.
      await browser.close();
    },
    { scope: "worker" },
  ],

  // The tray window's own page, reloaded so no state leaks between tests.
  page: async ({ cdp }, use) => {
    const context = cdp.contexts()[0];
    if (!context) throw new Error("the tray WebView exposes no browser context");
    let page = context.pages().find((p) => p.url().startsWith(appOrigin));
    if (!page) {
      page = await context.waitForEvent("page", {
        predicate: (p) => p.url().startsWith(appOrigin),
        timeout: 60_000,
      });
    }
    await page.goto(`${appOrigin}/`);
    await page.waitForLoadState("domcontentloaded");
    await use(page);
  },
});

/** bound calls a Go method bound in the tray by its fully qualified name. */
export async function bound<T>(page: Page, fqn: string, ...args: unknown[]): Promise<T> {
  // A string expression: the app bundle does not set window.wails, only the
  // runtime script served by the tray does. That second runtime replaces the
  // handlers in window._wails (event dispatch among them), so the app's
  // handlers are put back after the import; otherwise the app would no
  // longer receive the tray's events.
  const expr = `(async () => {
    const saved = { ...(window._wails || {}) };
    const m = await import("/wails/runtime.js");
    Object.assign(window._wails, saved);
    return await m.Call.ByName(${JSON.stringify(fqn)}, ...${JSON.stringify(args)});
  })()`;
  return (await page.evaluate(expr)) as T;
}

/** The labels of the tray menu's one cloud item. */
export type CloudItem = "Start cloud" | "Stop cloud" | "Starting cloud…" | "Stopping cloud…" | "Checking cloud…";

/** TrayModel as the e2e-only TrayTest service reports it. */
export type TrayModel = {
  icon: string;
  tooltip: string;
  status: string;
  cloud_item: CloudItem;
  /** "start" | "stop", or "" while the item is disabled. */
  cloud_action: string;
};

export function trayState(page: Page): Promise<TrayModel> {
  return bound<TrayModel>(page, "main.TrayTest.State");
}

/** trayClick runs the handler of the tray menu item with this label. */
export async function trayClick(page: Page, item: CloudItem): Promise<void> {
  await bound<unknown>(page, "main.TrayTest.Click", item);
}

/**
 * openView opens a view of the shell: the project list through "HLab" in
 * the top bar, Server Types, Settings and Logs through the gear
 * ("Administration") and the Administration sidebar.
 */
export async function openView(page: Page, name: "Projects" | "Server Types" | "Settings" | "Logs"): Promise<void> {
  const banner = page.getByRole("banner");
  if (name === "Projects") {
    await banner.getByRole("link", { name: "HLab" }).first().click();
  } else {
    // The gear is the right-most "Administration" link of the top bar; the
    // breadcrumb may name the area too.
    await banner.getByRole("link", { name: "Administration", exact: true }).last().click();
    await page.getByRole("navigation").getByRole("link", { name, exact: true }).click();
  }
  await expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();
}

/** rowAction opens the row menu of a table row and picks an action. */
export async function rowAction(row: Locator, action: string): Promise<void> {
  await row.getByRole("button", { name: "Actions" }).click();
  await row.page().getByRole("menuitem", { name: action, exact: true }).click();
}

/** tableRow is the row of the visible table that holds this exact cell text. */
export function tableRow(page: Page, cell: string): Locator {
  return page
    .getByRole("table")
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: cell, exact: true }) });
}

export type CmdResult = { code: number; stdout: string; stderr: string };

/** hlab runs bin\hlab.exe of this working copy (the dev profile). */
export function hlab(...args: string[]): Promise<CmdResult> {
  return new Promise((resolve) => {
    execFile(env("HLAB_E2E_HLAB"), args, { timeout: 10 * 60_000, windowsHide: true }, (err, stdout, stderr) => {
      const code = err ? (typeof err.code === "number" ? err.code : -1) : 0;
      resolve({ code, stdout: String(stdout), stderr: String(stderr) });
    });
  });
}

/** cloudState is the state `hlab cloud status --output json` reports. */
export async function cloudState(): Promise<string> {
  const r = await hlab("cloud", "status", "--output", "json");
  try {
    return (JSON.parse(r.stdout.trim()) as { state?: string }).state ?? "";
  } catch {
    return "";
  }
}

/** api sends one request to hlab's public API with a project token. */
export async function api(token: string, path: string): Promise<{ status: number; body: unknown }> {
  const res = await fetch(apiEndpoint + path, { headers: { Authorization: `Bearer ${token}` } });
  const text = await res.text();
  let body: unknown = text;
  try {
    body = JSON.parse(text);
  } catch {
    // not JSON; keep the text
  }
  return { status: res.status, body };
}

/** uniqueName gives a project or type name of this run ([a-z0-9-]). */
export function uniqueName(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}`;
}
