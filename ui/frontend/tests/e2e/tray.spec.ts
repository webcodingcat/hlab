import { test, expect, trayState, trayClick, cloudState } from "./fixtures";
import type { Page } from "@playwright/test";
import type { TrayModel } from "./fixtures";

// A cloud start boots the appliance; the harness allows it 15 minutes.
const stepTimeout = 15 * 60_000;

/** The cloud status pill in the top bar. */
function pill(page: Page) {
  return page.getByRole("banner").getByRole("status");
}

/**
 * waitTray polls the tray state until it reaches want and returns every
 * icon key seen on the way, so the steps in between can be checked.
 */
async function waitTray(page: Page, want: string): Promise<{ last: TrayModel; seen: string[] }> {
  const seen: string[] = [];
  let last: TrayModel | undefined;
  await expect
    .poll(
      async () => {
        last = await trayState(page);
        if (seen[seen.length - 1] !== last.icon) seen.push(last.icon);
        return last.icon;
      },
      { timeout: stepTimeout, intervals: [250] },
    )
    .toBe(want);
  return { last: last!, seen };
}

test("UI-01", async ({ page }) => {
  test.setTimeout(2 * stepTimeout + 5 * 60_000);

  // The harness hands the tests a running cloud.
  const start = await waitTray(page, "running");
  expect(start.last.can_start).toBe(false);
  expect(start.last.can_stop).toBe(true);
  expect(start.last.tooltip).toMatch(/running/i);
  expect(start.last.status).toMatch(/running/i);
  await expect(pill(page)).toHaveText(/running/i);
  expect(await cloudState()).toBe("running");

  // Stop through the tray menu handler.
  await trayClick(page, "Stop cloud");
  const stopped = await waitTray(page, "stopped");
  expect(stopped.seen.filter((i) => i !== "running" && i !== "busy" && i !== "stopped"), "icons between running and stopped").toEqual([]);
  expect(stopped.last.can_start).toBe(true);
  expect(stopped.last.can_stop).toBe(false);
  expect(stopped.last.tooltip).toMatch(/stopped/i);
  expect(stopped.last.status).toMatch(/stopped/i);
  await expect(pill(page)).toHaveText(/stopped/i, { timeout: 30_000 });
  expect(await cloudState()).toBe("stopped");

  // Start again through the tray menu handler.
  await trayClick(page, "Start cloud");
  const running = await waitTray(page, "running");
  expect(running.seen.filter((i) => i !== "stopped" && i !== "busy" && i !== "running"), "icons between stopped and running").toEqual([]);
  expect(running.last.can_start).toBe(false);
  expect(running.last.can_stop).toBe(true);
  expect(running.last.tooltip).toMatch(/running/i);
  expect(running.last.status).toMatch(/running/i);
  await expect(pill(page)).toHaveText(/running/i, { timeout: 30_000 });
  expect(await cloudState()).toBe("running");
});
