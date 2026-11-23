<script module lang="ts">
  /** SubaccountAccess are the access settings of a subaccount. */
  export type SubaccountAccess = {
    ssh_enabled: boolean;
    samba_enabled: boolean;
    webdav_enabled: boolean;
    readonly: boolean;
    reachable_externally: boolean;
  };

  /** Subaccount is a storage box subaccount as the Hetzner API returns it (the fields the tab uses). */
  export type Subaccount = {
    id: number;
    username: string;
    name: string;
    home_directory: string;
    description: string;
    access_settings: SubaccountAccess;
  };

  /** SubaccountValues are the values the dialog gives back; password is empty when editing. */
  export type SubaccountValues = {
    name: string;
    description: string;
    home_directory: string;
    password: string;
    access: SubaccountAccess;
  };

  /** accessFlags are the access settings in the order the dialog and the table show them. */
  export const accessFlags: { key: keyof SubaccountAccess; label: string }[] = [
    { key: "ssh_enabled", label: "SSH enabled" },
    { key: "samba_enabled", label: "Samba enabled" },
    { key: "webdav_enabled", label: "WebDAV enabled" },
    { key: "readonly", label: "Read-only" },
    { key: "reachable_externally", label: "Reachable externally" },
  ];
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import type { FormErrors } from "../../lib/formErrors";

  // The dialog to create a subaccount (sub undefined: with a password) or
  // to edit one (starts from its current values, no password). It is
  // created anew each time it opens; errors are shown under their fields.
  type Props = {
    open: boolean;
    sub?: Subaccount;
    errors: FormErrors;
    busy: boolean;
    onsubmit: (v: SubaccountValues) => void;
  };

  let { open = $bindable(), sub, errors, busy, onsubmit }: Props = $props();

  const initial = untrack(() => sub);
  let name = $state(initial?.name ?? "");
  let description = $state(initial?.description ?? "");
  let home = $state(initial?.home_directory ?? "");
  let password = $state("");
  let access = $state<SubaccountAccess>(
    initial
      ? { ...initial.access_settings }
      : { ssh_enabled: false, samba_enabled: false, webdav_enabled: false, readonly: false, reachable_externally: false },
  );

  const invalid = $derived(home.trim() === "" || (!initial && password === ""));

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (invalid || busy) return;
    onsubmit({ name: name.trim(), description, home_directory: home.trim(), password, access: { ...access } });
  }
</script>

<Dialog bind:open title={initial ? `Edit subaccount ${initial.username}` : "Create subaccount"}>
  <form onsubmit={submit}>
    <Field label="Name" error={errors.fields.name}>
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
      {/snippet}
    </Field>
    <Field label="Description" error={errors.fields.description}>
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={description} />
      {/snippet}
    </Field>
    <Field label="Home directory" error={errors.fields.home_directory} hint="A folder of the storage box, for example backups/web.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={home} />
      {/snippet}
    </Field>
    {#if !initial}
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
    {/if}
    <fieldset>
      <legend>Access settings</legend>
      {#each accessFlags as f (f.key)}
        <label class="check"><input type="checkbox" bind:checked={access[f.key]} />{f.label}</label>
      {/each}
      {#if errors.fields.access_settings}<div class="error">{errors.fields.access_settings}</div>{/if}
    </fieldset>
    {#if errors.general}<div class="error">{errors.general}</div>{/if}
    <div class="actions">
      <Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
      <Button type="submit" variant="primary" loading={busy} disabled={invalid}>{initial ? "Save" : "Create"}</Button>
    </div>
  </form>
</Dialog>

<style>
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    margin: 0 0 var(--s4);
    padding: 0;
    border: none;
  }
  legend {
    margin-bottom: var(--s1);
    font-weight: 600;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  input:not([type="checkbox"]) {
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
