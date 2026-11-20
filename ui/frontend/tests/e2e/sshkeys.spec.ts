import { generateKeyPairSync } from "node:crypto";
import { test, expect, rowAction, tableRow, api } from "./fixtures";
import { newProject, send, openConsole, section, helpOpens } from "./console";

type SshKey = { id: number; name: string; fingerprint: string; labels: Record<string, string> };
type ApiError = { error?: { code?: string; message?: string; details?: { fields?: { name: string; messages: string[] }[] } } };

/** publicKey makes a new ed25519 public key in OpenSSH format. */
function publicKey(comment: string): string {
  const { publicKey: key } = generateKeyPairSync("ed25519");
  const raw = Buffer.from(key.export({ format: "jwk" }).x!, "base64url");
  const field = (b: Buffer) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(b.length);
    return Buffer.concat([len, b]);
  };
  const blob = Buffer.concat([field(Buffer.from("ssh-ed25519")), field(raw)]);
  return `ssh-ed25519 ${blob.toString("base64")} ${comment}`;
}

async function keysNamed(token: string, name: string): Promise<SshKey[]> {
  const r = await api(token, `/ssh_keys?name=${encodeURIComponent(name)}`);
  if (r.status !== 200) throw new Error(`GET /ssh_keys: status ${r.status}`);
  return (r.body as { ssh_keys: SshKey[] }).ssh_keys;
}

test("UI-02 ssh keys", async ({ page }) => {
  test.setTimeout(10 * 60_000);
  const project = await newProject("e2e-ui02-keys");
  try {
    await openConsole(page, project.name);
    await section(page, "SSH Keys");
    await helpOpens(page);

    // An invalid public key: the API's message shows under "Public key".
    const invalid = "ssh-ed25519 not-a-key e2e";
    const refused = (await send(project.token, "POST", "/ssh_keys", { name: "ui02-probe", public_key: invalid })).body as ApiError;
    const messages = refused.error?.details?.fields?.find((f) => f.name === "public_key")?.messages ?? [];
    expect(messages.length, `the API names public_key in its error: ${JSON.stringify(refused)}`).toBeGreaterThan(0);

    const name = "ui02-key";
    await page.getByRole("button", { name: /^(Create|Add) SSH key$/ }).click();
    const keyField = page.getByLabel("Public key", { exact: true });
    await expect(keyField).toBeVisible();
    await keyField.fill(invalid);
    await page.getByLabel("Name", { exact: true }).fill(name);
    const submit = page.getByRole("button", { name: /^(Create|Add)( SSH key)?$/ });
    await submit.click();
    await expect(keyField).toHaveAccessibleDescription(new RegExp(messages.map((m) => m.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".*")));
    expect(await keysNamed(project.token, name)).toEqual([]);

    // A valid key is created and listed with the fingerprint the API reports.
    await keyField.fill(publicKey("ui02@e2e"));
    await submit.click();
    await expect.poll(async () => (await keysNamed(project.token, name)).length, { timeout: 30_000 }).toBe(1);
    const [created] = await keysNamed(project.token, name);
    expect(created.fingerprint).toBeTruthy();
    await section(page, "SSH Keys");
    const row = tableRow(page, name);
    await expect(row).toBeVisible();
    await expect(row.getByRole("cell", { name: created.fingerprint, exact: true })).toBeVisible();

    // A label added in the Labels tab is in the API.
    await row.getByRole("cell", { name, exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name, exact: true })).toBeVisible();
    await page.getByRole("link", { name: "Labels", exact: true }).click();
    await page.getByRole("button", { name: "Add label", exact: true }).click();
    await page.getByRole("textbox", { name: /key/i }).last().fill("env");
    await page.getByRole("textbox", { name: /value/i }).last().fill("e2e");
    await page.getByRole("button", { name: "Save labels", exact: true }).click();
    await expect
      .poll(async () => (await keysNamed(project.token, name))[0]?.labels, { timeout: 30_000 })
      .toEqual({ env: "e2e" });

    // Deleting it after the confirmation removes it from the list and the API.
    await section(page, "SSH Keys");
    await expect(row).toBeVisible();
    await rowAction(row, "Delete");
    await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(row).toBeHidden({ timeout: 30_000 });
    await expect.poll(async () => (await keysNamed(project.token, name)).length, { timeout: 30_000 }).toBe(0);
    expect((await api(project.token, `/ssh_keys/${created.id}`)).status).toBe(404);
  } finally {
    await project.remove();
  }
});
