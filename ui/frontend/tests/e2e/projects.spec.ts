import type { Page } from "@playwright/test";
import { test, expect, openView, rowAction, tableRow, api, uniqueName, hlab } from "./fixtures";
import { section, newProject, send, projectCard, openCard, type TestProject } from "./console";
import { href } from "../../src/lib/routes";

test("UI-05", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  await openView(page, "Projects");

  // Create a project.
  const project = uniqueName("ui05");
  await page.getByRole("button", { name: /New project/ }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByLabel("Display name").fill(project);
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  await expect(dialog).toBeHidden();
  const row = projectCard(page, project);
  await expect(row).toBeVisible();

  // A card click opens the project console of that project with "API tokens",
  // which its sidebar and section tabs open.
  await openCard(row);
  await expect(page.getByRole("banner").getByText(project).first()).toBeVisible();
  await section(page, "API tokens");

  // Create a token; its value is shown once.
  await page.getByRole("button", { name: "Create token" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("ui05");
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  const value = dialog.getByText(/^[A-Za-z0-9]{64}$/);
  await expect(value).toBeVisible();
  const token = (await value.textContent())!.trim();
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(token)).toHaveCount(0);

  // The token reads the project's API.
  expect((await api(token, "/servers")).status).toBe(200);

  // Revoke it; the API then refuses it.
  const tokenRow = tableRow(page, "ui05");
  await expect(tokenRow).toBeVisible();
  await rowAction(tokenRow, "Revoke");
  await page.getByRole("dialog").getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(tokenRow).toBeHidden();
  await expect.poll(async () => (await api(token, "/servers")).status, { timeout: 30_000 }).toBe(401);

  // The gear opens Administration and, clicked again, returns to this
  // project console.
  const gear = page.getByRole("banner").getByRole("link", { name: "Administration", exact: true });
  await gear.click();
  await expect(gear).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("banner").getByText("Administration", { exact: true })).toBeVisible();
  await gear.click();
  await expect(gear).not.toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "API tokens", exact: true })).toBeVisible();
  await expect(page.getByRole("banner").getByText(project).first()).toBeVisible();

  // Back in the list through the breadcrumb, delete the project after its
  // name is typed.
  await page.getByRole("banner").getByRole("link", { name: "Projects", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Projects", exact: true })).toBeVisible();
  await rowAction(projectCard(page, project), "Delete");
  dialog = page.getByRole("dialog");
  const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("textbox").fill(project);
  await confirm.click();
  await expect(projectCard(page, project)).toBeHidden({ timeout: 2 * 60_000 });
});

// UI-11: the project list as cards. The rows share three projects made
// through hlab, each with a display name of its own, created a second
// apart so their creation order differs from their display name order
// (Mike, Zulu, Alpha), and a project with one SSH key made through its API.

type Listed = { name: string; display: string };
type ListedProjects = { mike: Listed; zulu: Listed; alpha: Listed; keyed: TestProject };

/** createListed creates a project through hlab by its display name and returns the derived name. */
async function createListed(display: string): Promise<Listed> {
  const r = await hlab("project", "create", display);
  if (r.code !== 0) throw new Error(`hlab project create ${display}: exit ${r.code}: ${r.stderr}`);
  const m = /project (\S+) created/.exec(r.stdout);
  if (!m) throw new Error(`hlab project create ${display}: no project name in: ${r.stdout}`);
  return { name: m[1], display };
}

/** nextSecond waits until the clock is past the second it was called in, so the next project's creation time differs. */
async function nextSecond(): Promise<void> {
  const t = Date.now();
  await expect.poll(() => Date.now(), { timeout: 5_000, intervals: [100] }).toBeGreaterThan(t + 1_000);
}

const ui11 = test.extend<{}, { listed: ListedProjects }>({
  listed: [
    async ({}, use) => {
      const tag = Date.now().toString(36);
      const made: Listed[] = [];
      let keyed: TestProject | undefined;
      try {
        for (const word of ["Mike", "Zulu", "Alpha"]) {
          if (made.length > 0) await nextSecond();
          made.push(await createListed(`UI11 ${word} ${tag}`));
        }
        keyed = await newProject("e2e-ui11-key");
        const r = await send(keyed.token, "POST", "/ssh_keys", {
          name: "ui11",
          public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFXXbZ0g5SBrIE3ULtI/a/pEBnsJxOTFFK5NrzZVhQ8P ui11@e2e",
        });
        if (r.status !== 201) throw new Error(`POST /ssh_keys: status ${r.status}`);
        await use({ mike: made[0], zulu: made[1], alpha: made[2], keyed });
      } finally {
        for (const p of made) await hlab("project", "delete", p.name);
        if (keyed) await keyed.remove();
      }
    },
    { scope: "worker", timeout: 5 * 60_000 },
  ],
});

/** layout clicks the "Grid" or "List" side of the layout switch. */
async function layout(page: Page, name: "Grid" | "List"): Promise<void> {
  await page
    .getByRole("button", { name, exact: true })
    .or(page.getByRole("radio", { name, exact: true }))
    .click();
}

/** share is the width of the card of display as a part of the main area's width. */
async function share(page: Page, display: string): Promise<number> {
  const c = await projectCard(page, display).boundingBox();
  const m = await page.getByRole("main").boundingBox();
  return c && m ? c.width / m.width : 0;
}

/** sortBy picks an entry of the sort menu. */
async function sortBy(page: Page, key: "Display name" | "Created"): Promise<void> {
  await page.getByRole("button", { name: /^Sort/ }).click();
  await page.getByRole("menuitem", { name: key, exact: true }).click();
}

/** reverse clicks the direction arrow of the sort menu. */
async function reverse(page: Page): Promise<void> {
  await page.getByRole("button", { name: /ascending|descending|direction|reverse/i }).click();
}

/** domOrder gives the display names in the order the page shows them. */
async function domOrder(page: Page, displays: string[]): Promise<string[]> {
  const handles = await Promise.all(
    displays.map((d) => page.getByText(d, { exact: true }).elementHandle({ timeout: 5_000 })),
  );
  const idx = await page.evaluate(
    (els) =>
      els
        .map((_, i) => i)
        .sort((a, b) => (els[a]!.compareDocumentPosition(els[b]!) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1)),
    handles,
  );
  return idx.map((i) => displays[i]);
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

ui11("UI-11 layout", async ({ page, listed }) => {
  ui11.setTimeout(3 * 60_000);
  const shown = [listed.zulu, listed.alpha];
  await openView(page, "Projects");
  // The list refreshes every 5 s; the projects were made outside the window.
  for (const p of shown) await expect(projectCard(page, p.display)).toBeVisible({ timeout: 30_000 });

  // Grid: cards next to each other, each with display name and name.
  await layout(page, "Grid");
  for (const p of shown) {
    await expect(projectCard(page, p.display).getByText(p.name, { exact: true })).toBeVisible();
    await expect.poll(() => share(page, p.display), { timeout: 10_000 }).toBeLessThan(0.5);
  }

  // List: full-width rows with the same content.
  await layout(page, "List");
  for (const p of shown) {
    await expect(projectCard(page, p.display).getByText(p.name, { exact: true })).toBeVisible();
    await expect.poll(() => share(page, p.display), { timeout: 10_000 }).toBeGreaterThan(0.8);
  }

  // The layout survives a reload.
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Projects", exact: true })).toBeVisible({ timeout: 30_000 });
  for (const p of shown) {
    await expect(projectCard(page, p.display)).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => share(page, p.display), { timeout: 10_000 }).toBeGreaterThan(0.8);
  }

  // Back to the default.
  await layout(page, "Grid");
  await expect.poll(() => share(page, listed.zulu.display), { timeout: 10_000 }).toBeLessThan(0.5);
});

ui11("UI-11 sort", async ({ page, listed }) => {
  ui11.setTimeout(3 * 60_000);
  const { mike, zulu, alpha } = listed;
  const displays = [mike.display, zulu.display, alpha.display];
  const states: Record<string, string[]> = {
    "display_name asc": [alpha.display, mike.display, zulu.display],
    "display_name desc": [zulu.display, mike.display, alpha.display],
    "created asc": [mike.display, zulu.display, alpha.display],
    "created desc": [alpha.display, zulu.display, mike.display],
  };
  const state = async (): Promise<string> => {
    const o = await domOrder(page, displays);
    return Object.keys(states).find((k) => states[k].every((d, i) => o[i] === d)) ?? `none: ${o.join(", ")}`;
  };
  const opposite = (s: string) => (s.endsWith("asc") ? s.replace("asc", "desc") : s.replace("desc", "asc"));
  const settled = async (key: string): Promise<string> => {
    await expect.poll(state, { timeout: 10_000 }).toMatch(new RegExp(`^${key} (asc|desc)$`));
    return state();
  };

  await openView(page, "Projects");
  for (const d of displays) await expect(projectCard(page, d)).toBeVisible({ timeout: 30_000 });

  // Display name, and the arrow reverses it.
  await sortBy(page, "Display name");
  let s = await settled("display_name");
  await reverse(page);
  await expect.poll(state, { timeout: 10_000 }).toBe(opposite(s));
  s = opposite(s);

  // The choice survives a reload.
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Projects", exact: true })).toBeVisible({ timeout: 30_000 });
  for (const d of displays) await expect(projectCard(page, d)).toBeVisible({ timeout: 30_000 });
  await expect.poll(state, { timeout: 10_000 }).toBe(s);

  // Created, and the arrow reverses it.
  await sortBy(page, "Created");
  s = await settled("created");
  await reverse(page);
  await expect.poll(state, { timeout: 10_000 }).toBe(opposite(s));
  s = opposite(s);

  // Back to the default, created ascending.
  if (s !== "created asc") {
    await reverse(page);
    await expect.poll(state, { timeout: 10_000 }).toBe("created asc");
  }
});

ui11("UI-11 resources", async ({ page, listed }) => {
  ui11.setTimeout(3 * 60_000);
  await openView(page, "Projects");

  // One SSH key: "1 resource", whose menu opens the SSH Keys tab.
  const keyed = projectCard(page, listed.keyed.name);
  await expect(keyed.getByText("1 resource", { exact: true })).toBeVisible({ timeout: 30_000 });
  await keyed.getByRole("button", { name: "1 resource" }).click();
  await page.getByText("1 SSH Key", { exact: true }).click();
  const keys = href({ area: "project", project: listed.keyed.name, view: "ssh-keys", page: { kind: "list" } });
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(keys)}$`));
  await expect(page.getByRole("heading", { level: 1, name: "SSH Keys", exact: true })).toBeVisible();
  await expect(page.getByRole("banner").getByText(listed.keyed.name).first()).toBeVisible();

  // An empty project: "+ Create server" opens its server create form.
  await openView(page, "Projects");
  const empty = projectCard(page, listed.mike.display);
  await expect(empty).toBeVisible({ timeout: 30_000 });
  await empty.getByText(/Create server/).click();
  const create = href({ area: "project", project: listed.mike.name, view: "servers", page: { kind: "new" } });
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(create)}$`));
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();

  // The card's "…" menu has Delete.
  await openView(page, "Projects");
  const other = projectCard(page, listed.zulu.display);
  await expect(other).toBeVisible({ timeout: 30_000 });
  await other.getByRole("button", { name: "Actions" }).click();
  const del = page.getByRole("menuitem", { name: "Delete", exact: true });
  await expect(del).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(del).toBeHidden();
});

ui11("UI-11 new project", async ({ page }) => {
  ui11.setTimeout(4 * 60_000);
  const tag = Date.now().toString(36);
  const display = `Web Coding Cat ${tag}`;
  const derived = `web-coding-cat-${tag}`;
  try {
    await openView(page, "Projects");

    // The dashed tile opens "Create project" with the derived name shown live.
    const tile = page.getByRole("button", { name: /New project/ });
    await tile.click();
    let dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Create project", { exact: true }).first()).toBeVisible();
    await dialog.getByLabel("Display name").fill(display);
    await expect(dialog.getByText(new RegExp(`Name:\\s*${escapeRegExp(derived)}`)).first()).toBeVisible();
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect(dialog).toBeHidden();
    const created = projectCard(page, display);
    await expect(created).toBeVisible({ timeout: 30_000 });
    await expect(created.getByText(derived, { exact: true })).toBeVisible();

    // The same display name in lower case gives the same name: refused under the field.
    await tile.click();
    dialog = page.getByRole("dialog");
    const field = dialog.getByLabel("Display name");
    await field.fill(display.toLowerCase());
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    const refusal = dialog.getByText(/already exists/);
    await expect(refusal).toBeVisible({ timeout: 30_000 });
    await expect(refusal).toContainText(derived);
    const fieldBox = await field.boundingBox();
    const refusalBox = await refusal.boundingBox();
    expect(refusalBox!.y, "the refusal is under the field").toBeGreaterThanOrEqual(fieldBox!.y + fieldBox!.height - 1);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(dialog).toBeHidden();

    // Delete needs the display name typed, not the name.
    await rowAction(created, "Delete");
    dialog = page.getByRole("dialog");
    const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
    await dialog.getByRole("textbox").fill(derived);
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill(display);
    await confirm.click();
    await expect(projectCard(page, display)).toBeHidden({ timeout: 2 * 60_000 });
  } finally {
    // Gone already when the test passed; the result is not needed.
    await hlab("project", "delete", derived);
  }
});
