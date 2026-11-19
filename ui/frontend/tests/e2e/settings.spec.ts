import { readFileSync } from "node:fs";
import { test, expect, env, openView } from "./fixtures";
import type { Page } from "@playwright/test";

// settings.toml of the dev profile; the Go harness restores it afterwards.
function settingsFile(): string {
  return readFileSync(env("HLAB_E2E_SETTINGS"), "utf8");
}

/** tomlValue finds `key = value` in the given table of settings.toml. */
function tomlValue(text: string, table: string, key: string): string | undefined {
  let current = "";
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    const t = /^\[([^\]]+)\]$/.exec(line);
    if (t) {
      current = t[1].trim();
      continue;
    }
    const kv = /^([A-Za-z0-9_]+)\s*=\s*(.+?)\s*(#.*)?$/.exec(line);
    if (kv && current === table && kv[1] === key) {
      const v = kv[2];
      if (v.startsWith('"')) return (JSON.parse(v) as string);
      return v.replace(/^'(.*)'$/, "$1");
    }
  }
  return undefined;
}

async function save(page: Page) {
  await page.getByRole("button", { name: "Save settings" }).click();
}

test("UI-03", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Settings");

  const stop = page.getByLabel("Stop behaviour");
  const range = page.getByLabel("Public range");
  const folder = page.getByLabel("Data folder");
  const ram = page.getByLabel("Reserved RAM (MB)");
  const disk = page.getByLabel("Reserved disk (GB)");
  const level = page.getByLabel("Log level");
  const trust = page.getByRole("switch", { name: "Trust the hlab CA" });

  // Every key gets a value other than its current one.
  const nextStop = (await stop.inputValue()) === "save" ? "shutdown" : "save";
  const nextRange = (await range.inputValue()) === "198.18.0.0/16" ? "198.19.0.0/16" : "198.18.0.0/16";
  const nextFolder = `${env("ProgramData")}\\hlab-dev\\e2e-ui03-data`;
  const nextRam = String(Number(await ram.inputValue()) + 512);
  const nextDisk = String(Number(await disk.inputValue()) + 5);
  const nextLevel = (await level.inputValue()) === "debug" ? "info" : "debug";

  await stop.selectOption(nextStop);
  await range.fill(nextRange);
  await folder.fill(nextFolder);
  await ram.fill(nextRam);
  await disk.fill(nextDisk);
  await level.selectOption(nextLevel);
  await save(page);

  await expect
    .poll(() => {
      const f = settingsFile();
      return {
        stop_behaviour: tomlValue(f, "cloud", "stop_behaviour"),
        public_range: tomlValue(f, "network", "public_range"),
        data_folder: tomlValue(f, "storage", "data_folder"),
        reserve_ram_mb: tomlValue(f, "resources", "reserve_ram_mb"),
        reserve_disk_gb: tomlValue(f, "resources", "reserve_disk_gb"),
        level: tomlValue(f, "logging", "level"),
      };
    })
    .toEqual({
      stop_behaviour: nextStop,
      public_range: nextRange,
      data_folder: nextFolder,
      reserve_ram_mb: nextRam,
      reserve_disk_gb: nextDisk,
      level: nextLevel,
    });

  // trust_ca goes through the running cloud (PUT /trust), there and back.
  const trustBefore = (await trust.getAttribute("aria-checked")) === "true";
  for (const on of [!trustBefore, trustBefore]) {
    await trust.click();
    await expect(trust).toHaveAttribute("aria-checked", String(on), { timeout: 60_000 });
    await expect.poll(() => tomlValue(settingsFile(), "certificates", "trust_ca"), { timeout: 60_000 }).toBe(String(on));
  }

  // An invalid public range shows its message under the field and writes nothing.
  const before = settingsFile();
  await range.fill("not-a-range");
  await save(page);
  const describedBy = await range.getAttribute("aria-describedby");
  expect(describedBy, "the public range field names its error message").toBeTruthy();
  await expect(page.locator(`[id="${describedBy}"]`)).toBeVisible();
  await expect(page.locator(`[id="${describedBy}"]`)).not.toBeEmpty();
  expect(settingsFile()).toBe(before);
});
