import { execFile } from "node:child_process";
import type { Page } from "@playwright/test";
import { test, expect, env, api } from "./fixtures";
import { openConsole, section, openRow, menuAction } from "./console";

// UI-15 runs against the run's shared server, which the Go harness
// (test/e2e/ui_test.go) makes ready and names through the environment, with
// "Trust CA" switched off.

/** The canvas of noVNC in the console window, measured in the page. */
type Screen = { width: number; height: number; lit: number };

/**
 * windowTitled reports whether the desktop has a top-level window with
 * exactly this title, read through UI Automation (the native window title,
 * not the page's document title).
 */
function windowTitled(title: string): Promise<boolean> {
  const script =
    "Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes; " +
    "$c = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, $env:HLAB_E2E_TITLE); " +
    "if ([System.Windows.Automation.AutomationElement]::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children, $c)) { 'yes' } else { 'no' }";
  return new Promise((resolve) => {
    execFile(
      "powershell.exe",
      ["-NoProfile", "-NonInteractive", "-Command", script],
      { timeout: 30_000, windowsHide: true, env: { ...process.env, HLAB_E2E_TITLE: title } },
      (err, stdout) => resolve(!err && String(stdout).trim() === "yes"),
    );
  });
}

/**
 * consolePage waits for the tray page whose URL holds the console route; a
 * new window first shows about:blank (E24).
 */
async function consolePage(page: Page, route: string): Promise<Page> {
  let found: Page | undefined;
  await expect
    .poll(
      () => {
        found = page.context().pages().find((p) => p.url().includes(route));
        return found !== undefined;
      },
      { timeout: 30_000, message: `a page at ${route}` },
    )
    .toBe(true);
  return found!;
}

/** screen measures the noVNC canvas: its size and the pixels that are clearly not black. */
function screen(p: Page): Promise<Screen | null> {
  return p.evaluate(() => {
    const c = document.querySelector("canvas");
    const ctx = c?.getContext("2d");
    if (!c || !ctx || c.width === 0 || c.height === 0) return null;
    const d = ctx.getImageData(0, 0, c.width, c.height).data;
    let lit = 0;
    for (let i = 0; i + 3 < d.length; i += 4) {
      if (d[i] > 16 || d[i + 1] > 16 || d[i + 2] > 16) lit++;
    }
    return { width: c.width, height: c.height, lit };
  });
}

/** keepScreen stores the canvas pixels in the page for changedWidth. */
async function keepScreen(p: Page): Promise<void> {
  await p.evaluate(() => {
    const c = document.querySelector("canvas")!;
    const d = c.getContext("2d")!.getImageData(0, 0, c.width, c.height);
    (window as unknown as { __hlabKept: ImageData }).__hlabKept = d;
  });
}

/**
 * changedWidth is the width of the bounding box of the pixels that differ
 * from the kept screen; a changed size counts as the whole width.
 */
function changedWidth(p: Page): Promise<number> {
  return p.evaluate(() => {
    const kept = (window as unknown as { __hlabKept: ImageData }).__hlabKept;
    const c = document.querySelector("canvas");
    const ctx = c?.getContext("2d");
    if (!c || !ctx) return 0;
    if (c.width !== kept.width || c.height !== kept.height) return c.width;
    const d = ctx.getImageData(0, 0, c.width, c.height).data;
    const k = kept.data;
    let minX = c.width;
    let maxX = -1;
    for (let y = 0; y < c.height; y++) {
      for (let x = 0; x < c.width; x++) {
        const i = (y * c.width + x) * 4;
        if (d[i] !== k[i] || d[i + 1] !== k[i + 1] || d[i + 2] !== k[i + 2]) {
          if (x < minX) minX = x;
          if (x > maxX) maxX = x;
        }
      }
    }
    return maxX < 0 ? 0 : maxX - minX + 1;
  });
}

test("UI-15 console window shows the screen and sends keys", async ({ page }) => {
  test.setTimeout(4 * 60_000);
  const project = env("HLAB_E2E_PROJECT");
  const token = env("HLAB_E2E_API_TOKEN");
  const id = Number(env("HLAB_E2E_SERVER_ID"));
  const name = env("HLAB_E2E_SERVER_NAME");

  // The server is running.
  await expect
    .poll(async () => {
      const r = await api(token, `/servers/${id}`);
      return r.status === 200 ? (r.body as { server: { status: string } }).server.status : `status ${r.status}`;
    }, { timeout: 60_000 })
    .toBe("running");

  // "Console" in the server's actions menu opens the console window.
  await openConsole(page, project);
  await section(page, "Servers");
  await openRow(page, name);
  await menuAction(page, "Console");

  const title = `Console: ${name}`;
  await expect.poll(() => windowTitled(title), { timeout: 30_000, message: `a window titled ${title}` }).toBe(true);
  const win = await consolePage(page, `#/server-console/${project}/${id}`);
  try {
    // The screen shows a picture that is not black.
    await expect
      .poll(async () => (await screen(win))?.lit ?? 0, { timeout: 60_000, message: "lit pixels of the console screen" })
      .toBeGreaterThan(0);

    // Typed keys change it: a blinking cursor changes one character cell,
    // eight typed characters a region at least three cells (24 pixels) wide.
    await keepScreen(win);
    const canvas = win.locator("canvas").first();
    await canvas.click();
    const typed = "hlabhlab";
    await win.keyboard.type(typed);
    try {
      await expect
        .poll(() => changedWidth(win), { timeout: 30_000, message: "width of the screen region the typed keys changed" })
        .toBeGreaterThanOrEqual(24);
    } finally {
      // The login prompt gets its empty line back.
      for (let i = 0; i < typed.length; i++) await win.keyboard.press("Backspace");
    }
  } finally {
    // Close the console window, so the open console ends; a close that does
    // not finish in time is left to the tray's quit at the end of the run.
    await Promise.race([win.close().catch(() => undefined), new Promise((r) => setTimeout(r, 10_000))]);
  }
});
