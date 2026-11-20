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
 * waitTray polls the tray state until its icon reaches want and returns
 * every distinct state seen on the way, so the steps in between can be
 * checked.
 */
async function waitTray(page: Page, want: string): Promise<{ last: TrayModel; seen: TrayModel[] }> {
  const seen: TrayModel[] = [];
  let last: TrayModel | undefined;
  await expect
    .poll(
      async () => {
        last = await trayState(page);
        const prev = seen[seen.length - 1];
        if (!prev || prev.icon !== last.icon || prev.cloud_item !== last.cloud_item || prev.cloud_action !== last.cloud_action) {
          seen.push(last);
        }
        return last.icon;
      },
      { timeout: stepTimeout, intervals: [250] },
    )
    .toBe(want);
  return { last: last!, seen };
}

/** checkBusy checks that every busy state seen is disabled with its label. */
function checkBusy(seen: TrayModel[], label: "Stopping cloud…" | "Starting cloud…") {
  const busy = seen.filter((s) => s.icon === "busy");
  for (const s of busy) {
    expect(s.cloud_action, `cloud item "${s.cloud_item}" while busy`).toBe("");
    expect([label, "Checking cloud…"]).toContain(s.cloud_item);
  }
  expect(busy.map((s) => s.cloud_item), "busy labels seen").toContain(label);
}

test("UI-01", async ({ page }) => {
  test.setTimeout(2 * stepTimeout + 5 * 60_000);

  // The harness hands the tests a running cloud.
  const start = await waitTray(page, "running");
  expect(start.last.cloud_item).toBe("Stop cloud");
  expect(start.last.cloud_action).toBe("stop");
  expect(start.last.tooltip).toMatch(/^HLab: .*running/i);
  expect(start.last.status).toMatch(/running/i);
  await expect(pill(page)).toHaveText(/running/i);
  expect(await cloudState()).toBe("running");

  // Stop through the one cloud item.
  await trayClick(page, "Stop cloud");
  const stopped = await waitTray(page, "stopped");
  expect(stopped.seen.filter((s) => !["running", "busy", "stopped"].includes(s.icon)), "states between running and stopped").toEqual([]);
  checkBusy(stopped.seen, "Stopping cloud…");
  expect(stopped.last.cloud_item).toBe("Start cloud");
  expect(stopped.last.cloud_action).toBe("start");
  expect(stopped.last.tooltip).toMatch(/^HLab: .*stopped/i);
  expect(stopped.last.status).toMatch(/stopped/i);
  await expect(pill(page)).toHaveText(/stopped/i, { timeout: 30_000 });
  expect(await cloudState()).toBe("stopped");

  // The same item now starts the cloud again.
  await trayClick(page, "Start cloud");
  const running = await waitTray(page, "running");
  expect(running.seen.filter((s) => !["stopped", "busy", "running"].includes(s.icon)), "states between stopped and running").toEqual([]);
  checkBusy(running.seen, "Starting cloud…");
  expect(running.last.cloud_item).toBe("Stop cloud");
  expect(running.last.cloud_action).toBe("stop");
  expect(running.last.tooltip).toMatch(/^HLab: .*running/i);
  expect(running.last.status).toMatch(/running/i);
  await expect(pill(page)).toHaveText(/running/i, { timeout: 30_000 });
  expect(await cloudState()).toBe("running");
});
