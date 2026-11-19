<script lang="ts">
  import { TriangleAlert } from "@lucide/svelte";
  import Button from "../lib/Button.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import CopyField from "../lib/CopyField.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { ApiError, mgmt } from "../lib/bridge";
  import { action, showError } from "../lib/toast";
  import { running } from "../state";

  // Detail of one project: its API tokens. A new token's value is shown
  // once in the create dialog and dropped when the dialog closes.
  let { name }: { name: string } = $props();

  type Token = { name: string; read_only: boolean; created: string };

  let rows = $state<Token[] | undefined>();
  let failing = false;
  const base = $derived(`/projects/${encodeURIComponent(name)}/tokens`);

  const columns: Column<Token>[] = [
    { key: "name", label: "Name", value: (t) => t.name, sortable: true },
    { key: "access", label: "Access", value: (t) => (t.read_only ? "Read only" : "Read & write"), sortable: true },
    { key: "created", label: "Created", value: (t) => t.created.slice(0, 16).replace("T", " "), sortable: true },
  ];

  async function load() {
    try {
      const res = await mgmt<{ tokens: Token[] }>("GET", base);
      rows = res.tokens ?? [];
      failing = false;
    } catch (err) {
      if (!failing) showError("Load tokens", err);
      failing = true;
    }
  }

  $effect(() => {
    if (!$running) return;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Create dialog.
  let createDialog: HTMLDialogElement;
  let tokenName = $state("");
  let readOnly = $state(false);
  let nameError = $state<string | undefined>();
  let creating = $state(false);
  let value = $state<string | undefined>();

  function openCreate() {
    tokenName = "";
    readOnly = false;
    nameError = undefined;
    value = undefined;
    createDialog.showModal();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (creating) return;
    creating = true;
    nameError = undefined;
    try {
      const res = await mgmt<{ token: string }>("POST", base, { name: tokenName.trim(), read_only: readOnly });
      value = res.token;
      await load();
    } catch (err) {
      if (err instanceof ApiError && (err.code === "invalid_input" || err.code === "conflict")) {
        nameError = err.fields?.name?.join(" ") || err.message;
      } else {
        showError("Create token", err);
      }
    } finally {
      creating = false;
    }
  }

  // Revoke with confirm.
  let revoking = $state<Token | undefined>();
  let confirmOpen = $state(false);

  async function revoke() {
    const t = revoking!.name;
    await action(`Revoke token ${t}`, () => mgmt("DELETE", `${base}/${encodeURIComponent(t)}`));
    await load();
  }
</script>

<div class="title-row">
  <h1>{name}</h1>
  <HelpButton view="projects" />
</div>
<p><a href="#/projects">All projects</a></p>

{#if $running}
  <Table
    title="API tokens"
    {rows}
    {columns}
    key={(t) => t.name}
    name={(t) => t.name}
    emptyText="This project has no API tokens yet."
    createLabel="Create token"
    oncreate={openCreate}
    actions={(t) => [{ label: "Revoke", onclick: () => ((revoking = t), (confirmOpen = true)) }]}
  />
{:else}
  <p class="muted">Start the cloud to manage projects.</p>
{/if}

<dialog bind:this={createDialog} aria-labelledby="create-token-title" onclose={() => (value = undefined)}>
  <form onsubmit={create}>
    <h2 id="create-token-title">Create token</h2>
    {#if value}
      <p class="warning">
        <TriangleAlert size={16} aria-hidden="true" />
        Copy the token now. It is not shown again.
      </p>
      <CopyField {value} label="token" />
      <div class="actions">
        <Button variant="primary" onclick={() => createDialog.close()}>Close</Button>
      </div>
    {:else}
      <Field label="Name" error={nameError}>
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={tokenName} />
        {/snippet}
      </Field>
      <label class="switch">
        <input type="checkbox" role="switch" aria-checked={readOnly} bind:checked={readOnly} />
        Read only
      </label>
      <div class="actions">
        <Button onclick={() => createDialog.close()} disabled={creating}>Cancel</Button>
        <Button type="submit" variant="primary" loading={creating}>Create</Button>
      </div>
    {/if}
  </form>
</dialog>

<ConfirmDialog bind:open={confirmOpen} title="Revoke token" confirmLabel="Revoke" onconfirm={revoke}>
  <p>Clients using <strong>{revoking?.name}</strong> can no longer reach the API of {name}.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s2);
  }
  dialog {
    width: 520px;
    padding: var(--s6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.4);
  }
  h2 {
    margin-bottom: var(--s4);
  }
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .switch {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  .warning {
    display: flex;
    align-items: center;
    gap: var(--s2);
    color: var(--warning);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
