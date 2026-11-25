<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, hetznerApi, hetznerListAll, runHetznerOn } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { tabs, typesIn, type BoxType, type StorageBox } from "../../lib/resources/storageBoxes";
  import { showError } from "../../lib/toast";

  // The Settings tab of a storage box, one section with its own button
  // each: access settings, delete protection, change type (only the types
  // offered in the box's location) and reset password (a dialog; the
  // console never makes up or shows a password).
  let { project, box, refresh }: { project: string; box: StorageBox; refresh: () => void } = $props();

  type Type = BoxType & { description?: string };

  const accessFlags = [
    { key: "ssh_enabled", label: "SSH enabled" },
    { key: "samba_enabled", label: "Samba enabled" },
    { key: "webdav_enabled", label: "WebDAV enabled" },
    { key: "zfs_enabled", label: "ZFS enabled" },
    { key: "reachable_externally", label: "Reachable externally" },
  ];

  // The switches follow the refreshed box as long as nothing is changed;
  // base is the last state shown.
  let access = $state<Record<string, boolean>>({});
  let accessBase = "{}";
  let protect = $state(false);
  let protectBase = false;
  let boxType = $state("");
  let typeBase = "";

  $effect(() => {
    const nextAccess = Object.fromEntries(accessFlags.map((f) => [f.key, box.access_settings[f.key] ?? false]));
    const nextProtect = box.protection.delete;
    const nextType = box.storage_box_type.name;
    untrack(() => {
      if (JSON.stringify(access) === accessBase) access = nextAccess;
      accessBase = JSON.stringify(nextAccess);
      if (protect === protectBase) protect = nextProtect;
      protectBase = nextProtect;
      if (boxType === typeBase) boxType = nextType;
      typeBase = nextType;
    });
  });

  let types = $state<Type[]>([]);
  $effect(() => {
    hetznerListAll<Type>(project, "/storage_box_types", "storage_box_types")
      .then((t) => (types = t))
      .catch((err) => showError("Load the storage box types", err));
  });
  const offered = $derived(typesIn(types, box.location.name) as Type[]);

  let busy = $state<Record<string, boolean>>({});

  // run sends one tab action under the toast "Storage Box <name>: <label>".
  async function run(a: ResourceAction, input: Input) {
    const req = request(a, box.id, input);
    if (!req) return;
    busy[a.id] = true;
    const res = await runHetznerOn(project, box.name, a.label, req);
    busy[a.id] = false;
    if (res !== undefined) refresh();
  }

  function saveAccess(e: SubmitEvent) {
    e.preventDefault();
    run(tabs.update_access_settings, { ...access });
  }

  function saveProtection(e: SubmitEvent) {
    e.preventDefault();
    run(tabs.change_protection, { delete: protect });
  }

  function changeType(e: SubmitEvent) {
    e.preventDefault();
    run(tabs.change_type, { storage_box_type: boxType });
  }

  // Reset password: the API's field errors stay under the field in the
  // dialog; once the API accepts the request the dialog closes and the
  // toast follows the action.
  let resetOpen = $state(false);
  let password = $state("");
  let resetBusy = $state(false);
  let resetErrors = $state<FormErrors>({ fields: {} });

  function openReset() {
    password = "";
    resetErrors = { fields: {} };
    resetOpen = true;
  }

  async function resetPassword(e: SubmitEvent) {
    e.preventDefault();
    const req = request(tabs.reset_password, box.id, { password })!;
    // The project is taken now: the poll may outlive this tab.
    const p = project;
    resetBusy = true;
    resetErrors = { fields: {} };
    await runAction(
      actionTitle(resourceType(req.path), box.name, tabs.reset_password.label),
      async () => {
        try {
          const res = await hetznerApi<ActionResult>(p, req.method, req.path, req.body);
          resetOpen = false;
          password = "";
          return res;
        } catch (err) {
          resetErrors = formErrors(err, ["password"]);
          throw err;
        } finally {
          resetBusy = false;
        }
      },
      { get: async (id) => (await api<{ action: Action }>(p, "GET", `/actions/${id}`)).action },
    );
  }

  const flagId = $props.id();
</script>

<div class="sections">
  <form onsubmit={saveAccess}>
    <fieldset>
      <legend>Access settings</legend>
      {#each accessFlags as f (f.key)}
        <label class="check" for={`${flagId}-${f.key}`}>
          <input id={`${flagId}-${f.key}`} type="checkbox" bind:checked={access[f.key]} />
          {f.label}
        </label>
      {/each}
      <div class="actions">
        <Button type="submit" variant="primary" loading={busy.update_access_settings}>Save access settings</Button>
      </div>
    </fieldset>
  </form>

  <form onsubmit={saveProtection}>
    <fieldset>
      <legend>Delete protection</legend>
      <label class="check" for={`${flagId}-protect`}>
        <input id={`${flagId}-protect`} type="checkbox" bind:checked={protect} />
        Protect against deletion
      </label>
      <div class="actions">
        <Button type="submit" variant="primary" loading={busy.change_protection}>Save protection</Button>
      </div>
    </fieldset>
  </form>

  <form onsubmit={changeType}>
    <fieldset>
      <legend>Change type</legend>
      <Field label="Type" hint="Only the types offered in the storage box's location are listed.">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={boxType}>
            {#each offered as t (t.id)}
              <option value={t.name}>{t.description ? `${t.name} (${t.description})` : t.name}</option>
            {/each}
          </select>
        {/snippet}
      </Field>
      <div class="actions">
        <Button
          type="submit"
          variant="primary"
          loading={busy.change_type}
          disabled={boxType === "" || boxType === box.storage_box_type.name}>Change type</Button
        >
      </div>
    </fieldset>
  </form>

  <fieldset>
    <legend>Reset password</legend>
    <p class="muted">Sets a new password for the storage box's main account.</p>
    <div class="actions">
      <Button onclick={openReset}>Reset password</Button>
    </div>
  </fieldset>
</div>

<Dialog bind:open={resetOpen} title={`Reset password of ${box.name}`}>
  <form onsubmit={resetPassword}>
    <Field label="Password" error={resetErrors.fields.password}>
      {#snippet children(c)}
        <input
          id={c.id}
          type="password"
          autocomplete="new-password"
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          bind:value={password}
        />
      {/snippet}
    </Field>
    {#if resetErrors.general}<p class="error" role="alert">{resetErrors.general}</p>{/if}
    <div class="buttons">
      <Button onclick={() => (resetOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" loading={resetBusy} disabled={password === ""}>Reset password</Button>
    </div>
  </form>
</Dialog>

<style>
  .sections {
    display: flex;
    flex-direction: column;
    gap: var(--s6);
    max-width: 640px;
  }
  fieldset {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--s3);
    margin: 0;
    padding: var(--s4);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  legend {
    padding: 0 var(--s1);
    font-weight: 600;
  }
  fieldset :global(.field) {
    align-self: stretch;
  }
  select,
  input[type="password"] {
    width: 100%;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: var(--s2);
  }
  .actions {
    align-self: stretch;
    display: flex;
    justify-content: flex-end;
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
  .muted {
    color: var(--muted);
    margin: 0;
  }
  .error {
    color: var(--error);
  }
</style>
