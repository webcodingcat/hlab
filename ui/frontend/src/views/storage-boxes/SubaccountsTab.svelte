<script lang="ts">
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, hetznerApi, hetznerListAll, runHetzner } from "../../lib/api";
  import { actionTitle } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import type { StorageBox } from "../../lib/resources/storageBoxes";
  import { tabs } from "../../lib/resources/storageBoxSubaccounts";
  import { showError } from "../../lib/toast";
  import SubaccountDialog, { accessFlags, type Subaccount, type SubaccountValues } from "./SubaccountDialog.svelte";

  // The Subaccounts tab of a storage box: its subaccounts with username,
  // name, home directory, description and access. "Create subaccount" and
  // the row menu's Edit, Reset password and Delete work in dialogs; Edit
  // sends only the requests whose values changed, one after the other.
  let { project, box, refresh }: { project: string; box: StorageBox; refresh: () => void } = $props();

  // boxId changes only with the box, not with each refresh of its object.
  const boxId = $derived(box.id);
  let rows = $state<Subaccount[] | undefined>();
  let failing = false;

  const accessText = (s: Subaccount) =>
    accessFlags
      .filter((f) => s.access_settings[f.key])
      .map((f) => f.label)
      .join(", ") || "None";

  const columns: Column<Subaccount>[] = [
    { key: "username", label: "Username", value: (s) => s.username, sortable: true },
    { key: "name", label: "Name", value: (s) => s.name, sortable: true },
    { key: "home", label: "Home directory", value: (s) => s.home_directory, sortable: true },
    { key: "description", label: "Description", value: (s) => s.description },
    { key: "access", label: "Access", value: accessText },
  ];

  async function load() {
    try {
      rows = await hetznerListAll<Subaccount>(project, `/storage_boxes/${boxId}/subaccounts`, "subaccounts");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load subaccounts", err);
      failing = true;
    }
  }

  // The subaccounts refresh every 5 s while the tab is shown.
  $effect(() => {
    void boxId;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function changed() {
    load();
    refresh();
  }

  const poll = { get: async (id: number) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action };

  // send runs a request under an action toast; answered gets the API's
  // error, or nothing once the request is accepted (the toast follows the
  // action from there).
  function send(title: string, a: ResourceAction, input: Input, answered: (err?: unknown) => void) {
    const req = request(a, boxId, input)!;
    return runAction(
      title,
      async () => {
        try {
          const res = await hetznerApi<ActionResult>(project, req.method, req.path, req.body);
          answered();
          return res;
        } catch (err) {
          answered(err);
          throw err;
        }
      },
      poll,
    );
  }

  const subTitle = (s: Subaccount, action: string) => actionTitle("Subaccount", s.username, action);

  // Create and Edit share the dialog; editing holds the subaccount edited.
  let subOpen = $state(false);
  let editing = $state<Subaccount | undefined>();
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });
  const known = ["name", "description", "home_directory", "password", "access_settings"];

  function openSub(s?: Subaccount) {
    editing = s;
    errors = { fields: {} };
    busy = false;
    subOpen = true;
  }

  async function create(v: SubaccountValues) {
    busy = true;
    errors = { fields: {} };
    const input: Input = {
      home_directory: v.home_directory,
      password: v.password,
      name: v.name || undefined,
      description: v.description,
      access_settings: v.access,
    };
    const res = await send(actionTitle("Storage Box", box.name, tabs.create.label), tabs.create, input, (err) => {
      busy = false;
      if (err === undefined) subOpen = false;
      else errors = formErrors(err, known);
    });
    if (res !== undefined) changed();
  }

  // save closes the dialog and sends the changed parts in turn; a failed
  // request ends the chain (its toast tells why).
  async function save(s: Subaccount, v: SubaccountValues) {
    subOpen = false;
    const steps: { a: ResourceAction; input: Input }[] = [];
    if (v.name !== s.name || v.description !== s.description) {
      steps.push({ a: tabs.update, input: { name: v.name, description: v.description } });
    }
    if (v.home_directory !== s.home_directory) {
      steps.push({ a: tabs.change_home_directory, input: { home_directory: v.home_directory } });
    }
    if (accessFlags.some((f) => v.access[f.key] !== s.access_settings[f.key])) {
      steps.push({ a: tabs.update_access_settings, input: { ...v.access } });
    }
    for (const step of steps) {
      const req = request(step.a, boxId, { ...step.input, subaccount: s.id })!;
      const res = await runHetzner(project, subTitle(s, step.a.label), req.method, req.path, req.body);
      if (res === undefined) break;
    }
    if (steps.length > 0) changed();
  }

  function submitSub(v: SubaccountValues) {
    if (editing) save(editing, v);
    else create(v);
  }

  // Reset password: the password is entered; Hetzner's rule errors show under it.
  let resetOpen = $state(false);
  let resetting = $state<Subaccount | undefined>();
  let password = $state("");

  function openReset(s: Subaccount) {
    resetting = s;
    password = "";
    errors = { fields: {} };
    busy = false;
    resetOpen = true;
  }

  async function reset(e: SubmitEvent) {
    e.preventDefault();
    if (!resetting || password === "" || busy) return;
    const s = resetting;
    busy = true;
    errors = { fields: {} };
    await send(subTitle(s, tabs.reset_password.label), tabs.reset_password, { subaccount: s.id, password }, (err) => {
      busy = false;
      if (err === undefined) resetOpen = false;
      else errors = formErrors(err, ["password"]);
    });
  }

  // Delete: a plain confirm; the dialog closes at once, the toast follows.
  let deleteOpen = $state(false);
  let deleting = $state<Subaccount | undefined>();

  function askDelete(s: Subaccount) {
    deleting = s;
    deleteOpen = true;
  }

  function remove() {
    if (!deleting) return;
    const s = deleting;
    const req = request(tabs.delete, boxId, { subaccount: s.id })!;
    runHetzner(project, subTitle(s, tabs.delete.label), req.method, req.path).then((res) => {
      if (res !== undefined) changed();
    });
  }
</script>

<Table
  title="Subaccounts"
  {rows}
  {columns}
  key={(s) => s.id}
  name={(s) => `${s.name} ${s.username}`}
  emptyText="This storage box has no subaccounts yet."
  createLabel="Create subaccount"
  oncreate={() => openSub()}
  actions={(s) => [
    { label: "Edit", onclick: () => openSub(s) },
    { label: "Reset password", onclick: () => openReset(s) },
    { label: "Delete", onclick: () => askDelete(s) },
  ]}
/>

{#if subOpen}
  <SubaccountDialog bind:open={subOpen} sub={editing} {errors} {busy} onsubmit={submitSub} />
{/if}

<Dialog bind:open={resetOpen} title={`Reset password ${resetting?.username ?? ""}`}>
  <form onsubmit={reset}>
    <Field label="Password" error={errors.fields.password}>
      {#snippet children(c)}
        <input
          id={c.id}
          type="password"
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          autocomplete="new-password"
          bind:value={password}
        />
      {/snippet}
    </Field>
    {#if errors.general}<div class="error">{errors.general}</div>{/if}
    <div class="actions">
      <Button onclick={() => (resetOpen = false)} disabled={busy}>Cancel</Button>
      <Button type="submit" variant="primary" loading={busy} disabled={password === ""}>Reset password</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog bind:open={deleteOpen} title="Delete subaccount" confirmLabel="Delete" onconfirm={remove}>
  {#if deleting}
    <p>The subaccount <strong>{deleting.username}</strong> is deleted; it can no longer log in.</p>
  {/if}
</ConfirmDialog>

<style>
  input {
    width: 100%;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s2);
  }
</style>
