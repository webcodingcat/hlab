import type { Locator, Page } from "@playwright/test";
import { execFile } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test, expect, env, api, rowAction, tableRow } from "./fixtures";
import { newProject, send, openConsole, section, helpOpens, pick, openRow, addLabel, deleteRow, menuAction } from "./console";
import type { TestProject } from "./console";

/** The Hetzner API of hlab (storage boxes). */
const hetznerEndpoint = "http://api.hetzner.internal/hetzner/v1";

type Access = Record<string, boolean>;
type Box = {
  id: number;
  name: string;
  username: string | null;
  server: string | null;
  storage_box_type: { name: string };
  location: { name: string };
  access_settings: Access;
  snapshot_plan: { max_snapshots: number; minute: number; hour: number; day_of_week: number | null; day_of_month: number | null } | null;
  protection: { delete: boolean };
  labels: Record<string, string>;
};
type Subaccount = { id: number; name: string; username: string; home_directory: string; description: string; access_settings: Access };
type Snapshot = { id: number; name: string; description: string; created: string };
type BoxType = { name: string; prices: { location: string }[] };
type FieldErrors = { error?: { details?: { fields?: { name: string; messages: string[] }[] } } };

/** hetzner sends one request with a JSON body to hlab's Hetzner API. */
async function hetzner(token: string, method: string, path: string, body?: unknown): Promise<{ status: number; body: unknown }> {
  const res = await fetch(hetznerEndpoint + path, {
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

/** list reads the array key of a Hetzner API list. */
async function list<T>(token: string, path: string, key: string): Promise<T[]> {
  const r = await hetzner(token, "GET", path);
  if (r.status !== 200) throw new Error(`GET ${path}: status ${r.status}`);
  return (r.body as Record<string, T[]>)[key];
}

async function boxNamed(token: string, name: string): Promise<Box | undefined> {
  return (await list<Box>(token, `/storage_boxes?name=${encodeURIComponent(name)}`, "storage_boxes"))[0];
}

async function boxGet(token: string, id: number): Promise<Box | undefined> {
  const r = await hetzner(token, "GET", `/storage_boxes/${id}`);
  return r.status === 200 ? (r.body as { storage_box: Box }).storage_box : undefined;
}

async function subaccounts(token: string, box: number): Promise<Subaccount[]> {
  return list<Subaccount>(token, `/storage_boxes/${box}/subaccounts`, "subaccounts");
}

async function snapshots(token: string, box: number): Promise<Snapshot[]> {
  return list<Snapshot>(token, `/storage_boxes/${box}/snapshots`, "snapshots");
}

/** succeeded is the number of successful storage box actions of the project with this command. */
async function succeeded(token: string, command: string): Promise<number> {
  const actions = await list<{ command: string; status: string }>(token, "/storage_boxes/actions?per_page=50", "actions");
  return actions.filter((a) => a.command === command && a.status === "success").length;
}

/** idle waits until no action of the box runs, so the next change is not refused as locked. */
async function idle(token: string, box: number): Promise<void> {
  await expect
    .poll(async () => (await list<unknown>(token, `/storage_boxes/${box}/actions?status=running`, "actions")).length, { timeout: 5 * 60_000 })
    .toBe(0);
}

/** done waits for an action through the Cloud API's /actions/{id}, which also holds storage box actions. */
async function done(token: string, id: number): Promise<void> {
  await expect
    .poll(async () => ((await api(token, `/actions/${id}`)).body as { action?: { status: string } }).action?.status, { timeout: 15 * 60_000 })
    .toBe("success");
}

/** password gives a password following Hetzner's rule. */
function password(): string {
  return `E2e-${Math.random().toString(36).slice(2, 12)}-Zq9`;
}

/** createBox creates a bx11 box through the API in the run's server location and waits for it. */
async function createBox(token: string, name: string): Promise<Box> {
  const r = await hetzner(token, "POST", "/storage_boxes", {
    name,
    location: env("HLAB_E2E_SERVER_LOCATION"),
    storage_box_type: "bx11",
    password: password(),
    access_settings: { reachable_externally: true, ssh_enabled: true },
  });
  expect(r.status, `POST /storage_boxes: ${JSON.stringify(r.body)}`).toBe(201);
  const { storage_box: box, action } = r.body as { storage_box: Box; action: { id: number } };
  await done(token, action.id);
  return box;
}

/** dropBoxes deletes every box of the project, protected ones too, and waits until they are gone. */
async function dropBoxes(project: TestProject): Promise<void> {
  for (const box of await list<Box>(project.token, "/storage_boxes", "storage_boxes")) {
    await idle(project.token, box.id);
    if (box.protection.delete) {
      await hetzner(project.token, "POST", `/storage_boxes/${box.id}/actions/change_protection`, { delete: false });
      await idle(project.token, box.id);
    }
    await expect.poll(async () => (await hetzner(project.token, "DELETE", `/storage_boxes/${box.id}`)).status, { timeout: 2 * 60_000 }).not.toBe(423);
    await expect.poll(async () => (await hetzner(project.token, "GET", `/storage_boxes/${box.id}`)).status, { timeout: 5 * 60_000 }).toBe(404);
  }
}

/** flag is the checkbox or switch named name inside scope. */
function flag(scope: Page | Locator, name: RegExp): Locator {
  return scope.getByRole("checkbox", { name }).or(scope.getByRole("switch", { name })).first();
}

/** setFlag turns a checkbox or switch on or off. */
async function setFlag(control: Locator, on: boolean): Promise<void> {
  const state = async () => (await control.getAttribute("aria-checked")) === "true" || (await control.isChecked().catch(() => false));
  if ((await state()) !== on) await control.click();
  await expect.poll(state).toBe(on);
}

/** tab opens a tab of the detail page. */
async function tab(page: Page, name: string): Promise<void> {
  await page.getByRole("link", { name, exact: true }).click();
}

/** offered lists the names of the entries of the select or radio group labelled label. */
async function offered(page: Page, label: string): Promise<string[]> {
  const select = page.getByRole("combobox", { name: label, exact: true });
  if ((await select.count()) > 0) {
    return select.first().evaluate((el) => Array.from((el as HTMLSelectElement).options).map((o) => `${o.value} ${o.text}`.toLowerCase()));
  }
  const radios = page.getByRole("radiogroup", { name: label, exact: true }).getByRole("radio");
  const out: string[] = [];
  for (const r of await radios.all()) out.push(((await r.getAttribute("aria-label")) ?? (await r.evaluate((el) => (el as HTMLInputElement).labels?.[0]?.textContent ?? ""))).toLowerCase());
  return out;
}

/** keyLogin lists the box's home over sftp from the host with the run's private key only. */
function keyLogin(user: string, server: string): Promise<boolean> {
  const dir = mkdtempSync(join(tmpdir(), "hlab-ui12-"));
  const batch = join(dir, "batch");
  writeFileSync(batch, "pwd\n");
  const sftp = join(process.env.SystemRoot ?? "C:\\Windows", "System32", "OpenSSH", "sftp.exe");
  const args = [
    "-o", "BatchMode=yes",
    "-o", "PreferredAuthentications=publickey",
    "-o", "IdentitiesOnly=yes",
    "-i", env("HLAB_E2E_SSH_KEY"),
    "-o", "StrictHostKeyChecking=no",
    "-o", `UserKnownHostsFile=${join(dir, "known_hosts")}`,
    "-o", "ConnectTimeout=10",
    "-o", "LogLevel=ERROR",
    "-P", "22",
    "-b", batch,
    `${user}@${server}`,
  ];
  return new Promise((resolve) => {
    execFile(sftp, args, { timeout: 60_000, windowsHide: true }, (err) => {
      rmSync(dir, { recursive: true, force: true });
      resolve(!err);
    });
  });
}

test("UI-12", async ({ page }) => {
  test.setTimeout(18 * 60_000);
  const location = env("HLAB_E2E_SERVER_LOCATION");
  const project = await newProject("e2e-ui12");
  const token = project.token;
  try {
    const keyName = "ui12-key";
    const key = await send(token, "POST", "/ssh_keys", { name: keyName, public_key: readFileSync(env("HLAB_E2E_SSH_PUBKEY_FILE"), "utf8").trim() });
    expect(key.status, `POST /ssh_keys: ${JSON.stringify(key.body)}`).toBe(201);

    await openConsole(page, project.name);
    await section(page, "Storage Boxes");
    await helpOpens(page);

    // The form offers in the location only the types priced there.
    await page.getByRole("button", { name: "Create Storage Box", exact: true }).click();
    await pick(page, "Location", location);
    const types = await list<BoxType>(token, "/storage_box_types?per_page=50", "storage_box_types");
    const priced = (t: BoxType) => t.prices.some((p) => p.location === location);
    expect(types.filter(priced).map((t) => t.name)).toContain("bx11");
    await expect
      .poll(async () => {
        const entries = await offered(page, "Type");
        const shown = (name: string) => entries.some((e) => e.split(/[\s()]+/).includes(name.toLowerCase()));
        return types.filter((t) => shown(t.name) !== priced(t)).map((t) => t.name);
      }, { timeout: 30_000 })
      .toEqual([]);

    // A password against Hetzner's rule is refused under the field, with the API's message.
    const name = "ui12-box";
    const probe = await hetzner(token, "POST", "/storage_boxes", { name: "ui12-probe", location, storage_box_type: "bx11", password: "short" });
    if (probe.status === 201) await dropBoxes(project);
    const messages = (probe.body as FieldErrors).error?.details?.fields?.find((f) => f.name === "password")?.messages ?? [];
    expect(messages.length, `the API names password in its error: ${JSON.stringify(probe.body)}`).toBeGreaterThan(0);
    await page.getByLabel("Name", { exact: true }).fill(name);
    await pick(page, "Type", "bx11");
    const pw = page.getByLabel("Password", { exact: true });
    await pw.fill("short");
    await setFlag(page.getByRole("checkbox", { name: keyName, exact: true }), true);
    await setFlag(flag(page, /^SSH( enabled| support)?$/i), true);
    await setFlag(flag(page, /reachable externally|external reachability/i), true);
    const submit = page.getByRole("button", { name: /^Create( Storage Box)?$/ }).last();
    await submit.click();
    await expect(pw).toHaveAccessibleDescription(new RegExp(messages.map((m) => m.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".*")));
    expect(await boxNamed(token, name)).toBeUndefined();

    // With a valid password the box is created with the key and the access settings.
    await pw.fill(password());
    await submit.click();
    await expect.poll(async () => (await boxNamed(token, name))?.username ?? null, { timeout: 15 * 60_000, intervals: [5_000] }).not.toBeNull();
    const box = (await boxNamed(token, name))!;
    expect({ location: box.location.name, type: box.storage_box_type.name, ssh: box.access_settings.ssh_enabled, external: box.access_settings.reachable_externally })
      .toEqual({ location, type: "bx11", ssh: true, external: true });
    await expect.poll(() => keyLogin(box.username!, box.server!), { timeout: 3 * 60_000, intervals: [5_000] }).toBe(true);
    await section(page, "Storage Boxes");
    await expect(tableRow(page, name)).toBeVisible({ timeout: 30_000 });

    // Overview: username and hostname.
    await openRow(page, name);
    await expect(page.getByText(box.username!, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(box.server!, { exact: true }).first()).toBeVisible();

    // A subaccount with home data/web made through the API shows as folder data with web inside.
    await idle(token, box.id);
    const sub = await hetzner(token, "POST", `/storage_boxes/${box.id}/subaccounts`, {
      home_directory: "data/web",
      password: password(),
      access_settings: { ssh_enabled: true },
    });
    expect(sub.status, `POST subaccounts: ${JSON.stringify(sub.body)}`).toBe(201);
    await done(token, (sub.body as { action: { id: number } }).action.id);
    await tab(page, "Settings");
    await tab(page, "Overview");
    const data = page.getByText("data", { exact: true }).first();
    await expect(data).toBeVisible({ timeout: 30_000 });
    await data.click();
    await expect(page.getByLabel("Path", { exact: true })).toHaveValue(/^\/?data\/?$/);
    await expect(page.getByText("web", { exact: true }).first()).toBeVisible({ timeout: 30_000 });

    // Settings: Samba on, type bx21 and a new password, each in the API.
    await tab(page, "Settings");
    const access = page.getByRole("group", { name: "Access settings" });
    await setFlag(flag(access, /samba/i), true);
    await access.getByRole("button", { name: /^Save/ }).click();
    await expect.poll(async () => (await boxGet(token, box.id))?.access_settings.samba_enabled, { timeout: 5 * 60_000 }).toBe(true);
    await idle(token, box.id);

    const change = page.getByRole("group", { name: "Change type" });
    await pick(page, "Type", "bx21");
    await change.getByRole("button", { name: "Change type", exact: true }).click();
    await expect.poll(async () => (await boxGet(token, box.id))?.storage_box_type.name, { timeout: 5 * 60_000 }).toBe("bx21");
    await idle(token, box.id);

    const resets = await succeeded(token, "reset_password");
    await page.getByRole("group", { name: "Reset password" }).getByRole("button", { name: "Reset password", exact: true }).click();
    let dialog = page.getByRole("dialog");
    await dialog.getByLabel("Password", { exact: true }).fill(password());
    await dialog.getByRole("button", { name: "Reset password", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(() => succeeded(token, "reset_password"), { timeout: 5 * 60_000 }).toBe(resets + 1);
    await idle(token, box.id);

    // Delete protection on: Delete is disabled.
    const protection = page.getByRole("group", { name: "Delete protection" });
    await setFlag(flag(protection, /./), true);
    await protection.getByRole("button", { name: /^Save/ }).click();
    await expect.poll(async () => (await boxGet(token, box.id))?.protection.delete, { timeout: 5 * 60_000 }).toBe(true);
    await idle(token, box.id);
    await page.getByRole("button", { name: "Actions", exact: true }).first().click();
    await expect(page.getByRole("menuitem", { name: "Delete", exact: true })).toBeDisabled();
    await page.keyboard.press("Escape");

    // A label in the Labels tab and a rename through the actions menu reach the API.
    await addLabel(page);
    await expect.poll(async () => (await boxGet(token, box.id))?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });
    const renamed = "ui12-box-renamed";
    await menuAction(page, "Rename");
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Name", { exact: true }).fill(renamed);
    await dialog.getByRole("button", { name: /^(Rename|Save)$/ }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(async () => (await boxGet(token, box.id))?.name, { timeout: 30_000 }).toBe(renamed);
    await expect(page.getByRole("heading", { level: 1, name: renamed, exact: true })).toBeVisible({ timeout: 30_000 });

    // Protection off; Delete needs the name typed and removes the box.
    await tab(page, "Settings");
    await setFlag(flag(protection, /./), false);
    await protection.getByRole("button", { name: /^Save/ }).click();
    await expect.poll(async () => (await boxGet(token, box.id))?.protection.delete, { timeout: 5 * 60_000 }).toBe(false);
    await idle(token, box.id);
    await section(page, "Storage Boxes");
    await deleteRow(page, renamed, true);
    await expect.poll(async () => (await hetzner(token, "GET", `/storage_boxes/${box.id}`)).status, { timeout: 5 * 60_000 }).toBe(404);
  } finally {
    await dropBoxes(project);
    await project.remove();
  }
});

test("UI-13", async ({ page }) => {
  test.setTimeout(18 * 60_000);
  const project = await newProject("e2e-ui13");
  const token = project.token;
  try {
    const box = await createBox(token, "ui13-box");
    await openConsole(page, project.name);
    await section(page, "Storage Boxes");
    await openRow(page, "ui13-box");

    // Subaccounts: create a read-only one with name, home directory and password.
    await tab(page, "Subaccounts");
    const subName = "ui13-sub";
    await page.getByRole("button", { name: "Create subaccount", exact: true }).click();
    let dialog = page.getByRole("dialog");
    await dialog.getByLabel("Name", { exact: true }).fill(subName);
    await dialog.getByLabel("Home directory", { exact: true }).fill("ui13/home");
    await dialog.getByLabel("Password", { exact: true }).fill(password());
    await setFlag(flag(dialog, /read-?only/i), true);
    await dialog.getByRole("button", { name: /^Create( subaccount)?$/ }).click();
    await expect(dialog).toBeHidden();
    const subOf = async () => (await subaccounts(token, box.id)).find((s) => s.name === subName);
    const trim = (p: string | undefined) => p?.replace(/^\/+|\/+$/g, "");
    await expect
      .poll(async () => {
        const s = await subOf();
        return s && { home: trim(s.home_directory), readonly: s.access_settings.readonly };
      }, { timeout: 5 * 60_000 })
      .toEqual({ home: "ui13/home", readonly: true });
    const subRow = tableRow(page, subName);
    await expect(subRow).toBeVisible({ timeout: 30_000 });
    await idle(token, box.id);

    // Edit: description, home directory and read-only off.
    await rowAction(subRow, "Edit");
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Description", { exact: true }).fill("ui13 edited");
    await dialog.getByLabel("Home directory", { exact: true }).fill("ui13/other");
    await setFlag(flag(dialog, /read-?only/i), false);
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect
      .poll(async () => {
        const s = await subOf();
        return s && { description: s.description, home: trim(s.home_directory), readonly: s.access_settings.readonly };
      }, { timeout: 5 * 60_000 })
      .toEqual({ description: "ui13 edited", home: "ui13/other", readonly: false });
    await idle(token, box.id);

    // Reset password and Delete reach the API.
    const resets = await succeeded(token, "reset_subaccount_password");
    await rowAction(subRow, "Reset password");
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Password", { exact: true }).fill(password());
    await dialog.getByRole("button", { name: /^Reset( password)?$/ }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(() => succeeded(token, "reset_subaccount_password"), { timeout: 5 * 60_000 }).toBe(resets + 1);
    await idle(token, box.id);
    const subId = (await subOf())!.id;
    await rowAction(subRow, "Delete");
    await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(subRow).toBeHidden({ timeout: 2 * 60_000 });
    await expect.poll(async () => (await hetzner(token, "GET", `/storage_boxes/${box.id}/subaccounts/${subId}`)).status, { timeout: 2 * 60_000 }).toBe(404);
    await idle(token, box.id);

    // Snapshots: create two, the second after the first is done.
    await tab(page, "Snapshots");
    const createSnapshot = async (description: string, count: number) => {
      await page.getByRole("button", { name: "Create snapshot", exact: true }).click();
      const d = page.getByRole("dialog");
      await d.getByLabel("Description", { exact: true }).fill(description);
      await d.getByRole("button", { name: /^Create( snapshot)?$/ }).click();
      await expect(d).toBeHidden();
      await expect.poll(async () => (await snapshots(token, box.id)).length, { timeout: 5 * 60_000 }).toBe(count);
      await idle(token, box.id);
    };
    await createSnapshot("ui13 first", 1);
    await createSnapshot("", 2);
    const all = await snapshots(token, box.id);
    const first = all.find((s) => s.description === "ui13 first")!;
    const second = all.find((s) => s.id !== first.id)!;
    await expect(tableRow(page, first.name)).toBeVisible({ timeout: 30_000 });
    await expect(tableRow(page, second.name)).toBeVisible({ timeout: 30_000 });

    // Edit a description.
    await rowAction(tableRow(page, second.name), "Edit");
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Description", { exact: true }).fill("ui13 second");
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect
      .poll(async () => (await snapshots(token, box.id)).find((s) => s.id === second.id)?.description, { timeout: 30_000 })
      .toBe("ui13 second");
    await idle(token, box.id);

    // Roll back to the first: the confirmation names the second, which is gone afterwards.
    await rowAction(tableRow(page, first.name), "Roll back");
    dialog = page.getByRole("dialog");
    await expect(dialog).toContainText(second.name);
    await dialog.getByRole("button", { name: "Roll back", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(tableRow(page, second.name)).toBeHidden({ timeout: 5 * 60_000 });
    await expect.poll(async () => (await hetzner(token, "GET", `/storage_boxes/${box.id}/snapshots/${second.id}`)).status, { timeout: 5 * 60_000 }).toBe(404);
    await idle(token, box.id);

    // Delete the first.
    await rowAction(tableRow(page, first.name), "Delete");
    await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(tableRow(page, first.name)).toBeHidden({ timeout: 2 * 60_000 });
    await expect.poll(async () => (await hetzner(token, "GET", `/storage_boxes/${box.id}/snapshots/${first.id}`)).status, { timeout: 2 * 60_000 }).toBe(404);
    await idle(token, box.id);

    // Set plan: Sunday, 03:15, 5 snapshots.
    await page.getByRole("button", { name: "Set plan", exact: true }).click();
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Max snapshots", { exact: true }).fill("5");
    await dialog.getByLabel("Hour", { exact: true }).fill("3");
    await dialog.getByLabel("Minute", { exact: true }).fill("15");
    await pick(page, "Day of week", "Sunday");
    await dialog.getByRole("button", { name: /^(Save|Set plan)$/ }).click();
    await expect(dialog).toBeHidden();
    await expect
      .poll(async () => {
        const p = (await boxGet(token, box.id))?.snapshot_plan;
        return p && { day_of_week: p.day_of_week, hour: p.hour, minute: p.minute, max_snapshots: p.max_snapshots };
      }, { timeout: 5 * 60_000 })
      .toEqual({ day_of_week: 7, hour: 3, minute: 15, max_snapshots: 5 });
    await idle(token, box.id);

    // Disable plan after the confirmation.
    await page.getByRole("button", { name: "Disable plan", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Disable plan", exact: true }).click();
    await expect.poll(async () => (await boxGet(token, box.id))?.snapshot_plan, { timeout: 5 * 60_000 }).toBeNull();
    await expect(page.getByText("No plan", { exact: true })).toBeVisible({ timeout: 30_000 });
  } finally {
    await dropBoxes(project);
    await project.remove();
  }
});
