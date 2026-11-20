// README screenshots of the tray window (tools/screenshots, task
// docs:screenshots): the project list filtered to the sample projects
// demo-* and the "API tokens" section of demo-web, each in light and dark,
// written to HLAB_SCREENSHOTS_DIR. The tool creates the samples; only
// their names and the token's name are on screen, never a token value.
import path from "node:path";
import type { Page } from "@playwright/test";
import { test, expect, env, openView, tableRow } from "../e2e/fixtures";
import { section } from "../e2e/console";

const schemes = ["light", "dark"] as const;

async function shoot(page: Page, name: string): Promise<void> {
  // No hover or focus state on the pictures.
  await page.mouse.move(1199, 799);
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  for (const scheme of schemes) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.screenshot({ path: path.join(env("HLAB_SCREENSHOTS_DIR"), `${name}-${scheme}.png`) });
  }
}

test("README screenshots", async ({ page }) => {
  test.setTimeout(2 * 60_000);
  try {
    await page.setViewportSize({ width: 1200, height: 800 });
    await openView(page, "Projects");

    // Only the sample projects: every shown name starts with "demo-".
    await page.getByRole("searchbox", { name: "Search" }).fill("demo-");
    for (const p of ["demo-web", "demo-staging", "demo-ci"]) {
      await expect(tableRow(page, p)).toBeVisible();
    }
    const names = await page.getByRole("table").getByRole("row").locator("td:first-child").allTextContents();
    expect(names.every((n) => n.trim().startsWith("demo-"))).toBe(true);
    await shoot(page, "projects");

    // The project console of demo-web with its token "ci".
    await tableRow(page, "demo-web").getByRole("cell", { name: "demo-web", exact: true }).click();
    await section(page, "API tokens");
    await expect(tableRow(page, "ci")).toBeVisible();
    await shoot(page, "tokens");
  } finally {
    await page.emulateMedia({ colorScheme: null });
    const cdp = await page.context().newCDPSession(page);
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.detach();
  }
});
