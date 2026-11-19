// Playwright Test runs two kinds of frontend tests: unit tests of pure
// TypeScript modules without a browser, and the tray UI tests that connect
// to the real tray window over CDP (started by test/e2e/ui_test.go).
import { defineConfig } from "@playwright/test";

export default defineConfig({
  workers: 1,
  fullyParallel: false,
  reporter: "list",
  use: { trace: "off", video: "off", screenshot: "off" },
  projects: [
    { name: "unit", testDir: "tests/unit" },
    { name: "e2e", testDir: "tests/e2e" },
  ],
});
