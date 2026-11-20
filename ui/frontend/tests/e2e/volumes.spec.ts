import { expect, env, api } from "./fixtures";
import { resourceTest as test, openConsole, section, helpOpens, pick, named, openRow, addLabel, deleteRow, send, menuAction } from "./console";

type Volume = {
  id: number;
  name: string;
  size: number;
  server: number | null;
  format: string | null;
  location: { name: string };
  labels: Record<string, string>;
};

test("UI-02 volumes", async ({ page, shared }) => {
  test.setTimeout(10 * 60_000);
  const token = shared.token;
  const name = "ui02-vol";
  const serverName = "ui02-vol-server";
  const location = env("HLAB_E2E_SERVER_LOCATION");

  // The server the volume is bound to, made through the API and never started.
  const made = await send(token, "POST", "/servers", {
    name: serverName,
    server_type: env("HLAB_E2E_SERVER_TYPE"),
    image: env("HLAB_E2E_SERVER_IMAGE"),
    location,
    start_after_create: false,
  });
  expect(made.status, `POST /servers: ${JSON.stringify(made.body)}`).toBe(201);
  const { server: madeServer, action: createAction } = made.body as { server: { id: number }; action: { id: number } };
  const serverId = madeServer.id;
  try {
    // The server stays locked until its create action ends.
    await expect
      .poll(async () => ((await api(token, `/actions/${createAction.id}`)).body as { action: { status: string } }).action.status, {
        timeout: 5 * 60_000,
      })
      .toBe("success");
    await openConsole(page, shared.name);
    await section(page, "Volumes");
    await helpOpens(page);

    // The form creates a volume with Size (field and slider), Name and that Server.
    await page.getByRole("button", { name: "Create volume", exact: true }).click();
    const size = page.getByRole("spinbutton", { name: "Size", exact: true });
    const slider = page.getByRole("slider");
    await slider.fill("20");
    await expect(size).toHaveValue("20");
    await size.fill("12");
    await expect(slider).toHaveValue("12");
    await page.getByLabel("Name", { exact: true }).fill(name);
    await pick(page, "Server", serverName);
    await page.getByRole("button", { name: /^Create( volume)?$/ }).last().click();
    // Server and format are set once the volume's create action ends.
    await expect
      .poll(
        async () => {
          const [v] = await named<Volume>(token, "/volumes", "volumes", name);
          return v && { size: v.size, server: v.server, format: v.format, location: v.location.name };
        },
        { timeout: 2 * 60_000 },
      )
      .toEqual({ size: 12, server: serverId, format: "ext4", location });
    const [created] = await named<Volume>(token, "/volumes", "volumes", name);
    await section(page, "Volumes");

    // A label added in its Labels tab is in the API.
    await openRow(page, name);
    await addLabel(page);
    await expect.poll(async () => (await named<Volume>(token, "/volumes", "volumes", name))[0]?.labels, { timeout: 30_000 }).toEqual({ env: "e2e" });

    // An attached volume cannot be deleted: Detach it through its actions menu first.
    await menuAction(page, "Detach");
    await page.getByRole("dialog").getByRole("button", { name: "Detach", exact: true }).click();
    await expect.poll(async () => (await named<Volume>(token, "/volumes", "volumes", name))[0]?.server, { timeout: 2 * 60_000 }).toBeNull();

    // Delete needs the name typed and removes it from the list and the API.
    await section(page, "Volumes");
    await deleteRow(page, name, true);
    await expect.poll(async () => (await api(token, `/volumes/${created.id}`)).status, { timeout: 2 * 60_000 }).toBe(404);
  } finally {
    // A running action of the server or its volume locks it for a while.
    await expect.poll(async () => (await send(token, "DELETE", `/servers/${serverId}`)).status, { timeout: 2 * 60_000 }).not.toBe(423);
    await expect.poll(async () => (await api(token, `/servers/${serverId}`)).status, { timeout: 2 * 60_000 }).toBe(404);
  }
});
