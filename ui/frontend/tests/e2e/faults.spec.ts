import type { Locator, Page } from "@playwright/test";
import { test, expect, env, api, hlab, rowAction, tableRow } from "./fixtures";
import { openConsole, section, pick } from "./console";

// UI-16 runs on the fault fixture of the FAULT rows (server A with its
// mounted volume, servers B and C, the storage box), which the Go harness
// (test/e2e/ui_test.go) names through the environment. UI-17 runs on two
// small projects the harness gives faults through `hlab fault create`.

type Fault = {
  id: number;
  project: string;
  kind: string;
  target: { type: string; id?: number; from?: string; to?: string } | null;
  params: Record<string, string | number> | null;
  seed: number | null;
  ends: string | null;
};

/** faultList is `hlab fault list --output json`, of one project or, without one, of all. */
async function faultList(project?: string): Promise<Fault[]> {
  const args = ["fault", "list", "--output", "json"];
  if (project) args.push("--project", project);
  const r = await hlab(...args);
  if (r.code !== 0) throw new Error(`hlab ${args.join(" ")}: exit ${r.code}: ${r.stderr}`);
  return (JSON.parse(r.stdout.trim()) as { faults: Fault[] | null }).faults ?? [];
}

/** column is the cell of row under the table header matching header. */
async function column(page: Page, row: Locator, header: RegExp): Promise<Locator> {
  const headers = await page.getByRole("table").getByRole("columnheader").allInnerTexts();
  const i = headers.findIndex((h) => header.test(h.trim()));
  expect(i, `a column ${header} among ${JSON.stringify(headers)}`).toBeGreaterThanOrEqual(0);
  return row.getByRole("cell").nth(i);
}

/** openForm opens the create form through the toolbar's "Create fault" and picks the kind. */
async function openForm(page: Page, kind: string): Promise<void> {
  await page.getByRole("button", { name: "Create fault", exact: true }).first().click();
  await pick(page, "Kind", kind);
}

/** submit creates the fault of the open form and waits for its row in the list. */
async function submit(page: Page, kind: string): Promise<Locator> {
  await page.getByRole("button", { name: /^Create( fault)?$/ }).last().click();
  await expect(page.getByRole("heading", { level: 1, name: "Faults", exact: true })).toBeVisible({ timeout: 30_000 });
  const row = tableRow(page, kind);
  await expect(row).toBeVisible({ timeout: 30_000 });
  return row;
}

/** created is the one fault of kind in the project's list. */
async function created(project: string, kind: string): Promise<Fault> {
  const of = (await faultList(project)).filter((f) => f.kind === kind);
  expect(of, `hlab fault list has one ${kind} fault`).toHaveLength(1);
  return of[0];
}

/** shows checks a row's target, parameter line and remaining time. */
async function shows(page: Page, row: Locator, target: string, params: string, left: string | RegExp): Promise<void> {
  await expect(await column(page, row, /^Target$/i)).toHaveText(target);
  await expect(await column(page, row, /^Param/i)).toHaveText(params);
  await expect(row).toContainText(left);
}

test("UI-16 project faults: create every kind through the form, end one, end all", async ({ page }) => {
  test.setTimeout(12 * 60_000);
  const project = env("HLAB_E2E_PROJECT");
  const token = env("HLAB_E2E_API_TOKEN");
  const a = { id: Number(env("HLAB_E2E_SERVER_A_ID")), name: env("HLAB_E2E_SERVER_A_NAME") };
  const b = { id: Number(env("HLAB_E2E_SERVER_B_ID")), name: env("HLAB_E2E_SERVER_B_NAME") };
  const c = { id: Number(env("HLAB_E2E_SERVER_C_ID")), name: env("HLAB_E2E_SERVER_C_NAME") };
  const volume = { id: Number(env("HLAB_E2E_VOLUME_ID")), name: env("HLAB_E2E_VOLUME_NAME") };
  const box = { id: Number(env("HLAB_E2E_STORAGE_BOX_ID")), name: env("HLAB_E2E_STORAGE_BOX_NAME") };
  const from = env("HLAB_E2E_SERVER_LOCATION");
  const to = env("HLAB_E2E_OTHER_LOCATION");

  // A freeze needs a running VM: server C runs.
  await expect
    .poll(async () => {
      const r = await api(token, `/servers/${c.id}`);
      return r.status === 200 ? (r.body as { server: { status: string } }).server.status : `status ${r.status}`;
    }, { timeout: 5 * 60_000 })
    .toBe("running");
  expect(await faultList(project), "no fault is active before UI-16").toEqual([]);

  // "Faults" in the sidebar shows the empty state.
  await openConsole(page, project);
  await section(page, "Faults");
  await expect(page.getByText("No faults are active in this project.", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "Create fault", exact: true }).first()).toBeVisible();

  // Server freeze on C, then "End" in its row menu removes it.
  await openForm(page, "Server freeze");
  await pick(page, "Target", c.name);
  let row = await submit(page, "Server freeze");
  const freeze = await created(project, "server_freeze");
  expect(freeze.target).toMatchObject({ type: "server", id: c.id });
  await shows(page, row, c.name, "–", "until ended");
  await expect(await column(page, row, /^Seed$/i)).toHaveText(/^\s*$/);
  await rowAction(row, "End");
  await expect(row).toBeHidden({ timeout: 30_000 });
  await expect.poll(async () => (await faultList(project)).map((f) => f.id), { timeout: 30_000 }).not.toContain(freeze.id);

  // Server crash on C, after the freeze has ended.
  await openForm(page, "Server crash");
  await pick(page, "Target", c.name);
  row = await submit(page, "Server crash");
  expect((await created(project, "server_crash")).target).toMatchObject({ type: "server", id: c.id });
  await shows(page, row, c.name, "–", "until ended");

  // Network on A: private traffic, 20 % loss, seed 42.
  await openForm(page, "Network");
  await pick(page, "Target", a.name);
  await pick(page, "Traffic", "Private");
  await page.getByLabel(/^Loss percent/).fill("20");
  await page.getByLabel(/^Seed/).fill("42");
  row = await submit(page, "Network");
  const network = await created(project, "network");
  expect({ target: network.target, params: network.params, seed: network.seed }).toEqual({
    target: { type: "server", id: a.id },
    params: { traffic: "private", loss_percent: 20 },
    seed: 42,
  });
  await shows(page, row, a.name, "private, 20 % loss", "until ended");
  await expect(await column(page, row, /^Seed$/i)).toHaveText("42");

  // Latency between the two locations, 50 ms with 10 ms jitter.
  await openForm(page, "Latency");
  await page.getByRole("radio", { name: "Between locations", exact: true }).check();
  await pick(page, "From", from);
  await pick(page, "To", to);
  await page.getByLabel(/^Delay/).fill("50");
  await page.getByLabel(/^Jitter/).fill("10");
  row = await submit(page, "Latency");
  const latency = await created(project, "latency");
  expect({ target: latency.target, params: latency.params }).toEqual({
    target: { type: "locations", from, to },
    params: { delay_ms: 50, jitter_ms: 10 },
  });
  await shows(page, row, `${from} ↔ ${to}`, "50 ms ± 10 ms", "until ended");

  // API error without seed: a percent of 0 is refused under the field, 20 is
  // created and HLab chooses the seed the table shows.
  await openForm(page, "API error");
  await pick(page, "Error code", "server_error");
  const percent = page.getByLabel(/^Percent/);
  await percent.fill("0");
  await pick(page, "Only", "Writes");
  await pick(page, "Resource type", "servers");
  await page.getByRole("button", { name: /^Create( fault)?$/ }).last().click();
  await expect(percent).toHaveAccessibleDescription(/100/, { timeout: 30_000 });
  expect((await faultList(project)).filter((f) => f.kind === "api_error")).toEqual([]);
  await percent.fill("20");
  row = await submit(page, "API error");
  const apiError = await created(project, "api_error");
  expect({ target: apiError.target, params: apiError.params }).toEqual({
    target: null,
    params: { code: "server_error", percent: 20, only: "write", resource: "servers" },
  });
  expect(apiError.seed, "the seed HLab chose").not.toBeNull();
  await shows(page, row, "Project", "20 % server_error, writes, servers", "until ended");
  await expect(await column(page, row, /^Seed$/i)).toHaveText(String(apiError.seed));

  // Rate limit for 5 minutes. 3600 per hour keeps the tray's own reads of
  // the project going while the fault is active.
  await openForm(page, "Rate limit");
  await page.getByLabel(/per hour/i).fill("3600");
  await pick(page, "Duration", "5m");
  row = await submit(page, "Rate limit");
  const rate = await created(project, "rate_limit");
  expect({ params: rate.params, ends: rate.ends !== null }).toEqual({ params: { per_hour: 3600 }, ends: true });
  await shows(page, row, "Project", "3600 per hour", /\b(?:[1-5] min|\d{1,2} s) left\b/);
  await expect(await column(page, row, /^Seed$/i)).toHaveText(/^\s*$/);

  // Volume unavailable on A's attached volume.
  await openForm(page, "Volume unavailable");
  await pick(page, "Target", volume.name);
  row = await submit(page, "Volume unavailable");
  expect((await created(project, "volume_unavailable")).target).toMatchObject({ type: "volume", id: volume.id });
  await shows(page, row, volume.name, "–", "until ended");

  // Slow disk on B's system disk.
  await openForm(page, "Slow disk");
  await pick(page, "Target", b.name);
  await page.getByLabel(/IOPS/).fill("200");
  row = await submit(page, "Slow disk");
  const disk = await created(project, "disk_slow");
  expect({ target: disk.target, params: disk.params }).toEqual({ target: { type: "server", id: b.id }, params: { iops: 200 } });
  await shows(page, row, b.name, "200 IOPS", "until ended");

  // Storage box unavailable on the box.
  await openForm(page, "Storage box unavailable");
  await pick(page, "Target", box.name);
  row = await submit(page, "Storage box unavailable");
  expect((await created(project, "storage_box_unavailable")).target).toMatchObject({ type: "storage_box", id: box.id });
  await shows(page, row, box.name, "–", "until ended");

  // Every kind is listed once more after the list's refresh.
  expect((await faultList(project)).map((f) => f.kind).sort()).toEqual(
    ["server_crash", "network", "latency", "api_error", "rate_limit", "volume_unavailable", "disk_slow", "storage_box_unavailable"].sort(),
  );

  // "End all" with its confirm dialog leaves the empty state and no fault.
  await page.getByRole("button", { name: "End all", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: /^End( all)?$/ }).click();
  await expect(page.getByText("No faults are active in this project.", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => faultList(project), { timeout: 30_000 }).toEqual([]);
});

test("UI-17 active faults: the top bar pill, the list of every project, end one, end all", async ({ page }) => {
  test.setTimeout(4 * 60_000);
  const projects = [env("HLAB_E2E_PROJECT_A"), env("HLAB_E2E_PROJECT_B")];
  const banner = page.getByRole("banner");
  const pill = banner.getByRole("link", { name: /^\d+ faults? active$/ });
  const pillText = (n: number) => (n === 1 ? "1 fault active" : `${n} faults active`);

  // The top bar shows the number of all active faults.
  const all = await faultList();
  for (const p of projects) expect(all.some((f) => f.project === p), `a fault of ${p}`).toBe(true);
  await expect(pill).toHaveText(pillText(all.length), { timeout: 30_000 });

  // A click opens "Active faults" with each fault and its project.
  await pill.click();
  await expect(page.getByRole("heading", { level: 1, name: "Active faults", exact: true })).toBeVisible();
  const kinds: Record<string, string> = { api_error: "API error", rate_limit: "Rate limit" };
  const rowOf = (f: Fault) =>
    page
      .getByRole("table")
      .getByRole("row")
      .filter({ has: page.getByRole("link", { name: f.project, exact: true }) })
      .filter({ has: page.getByRole("cell", { name: kinds[f.kind], exact: true }) });
  const ours = all.filter((f) => projects.includes(f.project));
  for (const f of ours) {
    const row = rowOf(f);
    await expect(row).toHaveCount(1, { timeout: 30_000 });
    await expect(row.getByRole("link", { name: f.project, exact: true })).toHaveAttribute("href", `#/projects/${f.project}/faults`);
  }

  // The project link opens that project's Faults.
  await rowOf(ours[0]).getByRole("link", { name: ours[0].project, exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Faults", exact: true })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`#/projects/${ours[0].project}/faults$`));
  await pill.click();
  await expect(page.getByRole("heading", { level: 1, name: "Active faults", exact: true })).toBeVisible();

  // "End" in a row ends that fault; the pill's number follows.
  const ended = ours.find((f) => f.project === projects[1])!;
  await rowAction(rowOf(ended), "End");
  await expect(rowOf(ended)).toHaveCount(0, { timeout: 30_000 });
  await expect.poll(async () => (await faultList()).map((f) => f.id), { timeout: 30_000 }).not.toContain(ended.id);
  await expect(pill).toHaveText(pillText((await faultList()).length), { timeout: 30_000 });

  // "End all" with its confirm dialog ends every fault; the pill disappears.
  await page.getByRole("button", { name: "End all", exact: true }).first().click();
  await page.getByRole("dialog").getByRole("button", { name: /^End( all)?$/ }).click();
  await expect(page.getByText("No faults are active.", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => faultList(), { timeout: 30_000 }).toEqual([]);
  await expect(pill).toBeHidden({ timeout: 30_000 });
});
