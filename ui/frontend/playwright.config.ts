// Playwright Test runs two kinds of frontend tests: unit tests of pure
// TypeScript modules without a browser, and the tray UI tests that connect
// to the real tray window over CDP (started by test/e2e/ui_test.go), and
// the README screenshots (tools/screenshots, task docs:screenshots).
import { defineConfig } from "@playwright/test";

export default defineConfig({
  workers: 1,
  fullyParallel: false,
  reporter: "list",
  use: { trace: "off", video: "off", screenshot: "off" },
  projects: [
    { name: "unit", testDir: "tests/unit" },
    // Every click, fill and select gets a deadline, so a control that never
    // becomes actionable fails with its locator instead of holding the run.
    { name: "e2e", testDir: "tests/e2e", use: { actionTimeout: 60_000 } },
    { name: "screenshots", testDir: "tests/screenshots" },
  ],
});
