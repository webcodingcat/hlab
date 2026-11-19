<script lang="ts">
  import Button from "../lib/Button.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { mgmt, service } from "../lib/bridge";
  import { action, showError } from "../lib/toast";
  import { running } from "../state";

  // The list comes from the bridge, which merges the shipped catalog, the
  // updated catalog and the own types the way hlabd does, so it shows while
  // the cloud is stopped too.
  type ServerType = {
    id: number;
    name: string;
    description: string;
    cores: number;
    memory: number;
    disk: number;
    cpu_type: string;
    locations?: { name: string }[] | null;
  };
  type Row = ServerType & { source: "catalog" | "own" };
  type TypeList = { types: { type: ServerType; source: "catalog" | "own" }[]; problem?: string };
  type Invalid = { field: string; message: string } | null;

  let rows = $state<Row[] | undefined>();
  let problem = $state("");
  let failing = false;

  const columns: Column<Row>[] = [
    { key: "name", label: "Name", value: (t) => t.name, sortable: true },
    { key: "cores", label: "Cores", value: (t) => t.cores, sortable: true },
    { key: "memory", label: "Memory (GB)", value: (t) => t.memory, sortable: true },
    { key: "disk", label: "Disk (GB)", value: (t) => t.disk, sortable: true },
    { key: "cpu_type", label: "CPU type", value: (t) => t.cpu_type, sortable: true },
    { key: "locations", label: "Locations", value: (t) => (t.locations ?? []).map((l) => l.name).join(", ") },
    { key: "source", label: "Source", value: (t) => t.source, sortable: true },
  ];

  async function load() {
    try {
      const list = await service<TypeList>("Service", "ServerTypes");
      rows = list.types.map((t) => ({ ...t.type, source: t.source }));
      problem = list.problem ?? "";
      failing = false;
    } catch (err) {
      if (!failing) showError("Load server types", err);
      failing = true;
    }
  }

  // Lists refresh every 5 seconds while visible.
  $effect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Add dialog; the field names are those of catalog.OwnSpec.
  let addDialog: HTMLDialogElement;
  const blank = () => ({ name: "", description: "", cores: 2, memory: 4, disk: 40, cpu_type: "shared", locations: "" });
  let spec = $state(blank());
  let errors = $state<Record<string, string>>({});
  let adding = $state(false);

  function openAdd() {
    spec = blank();
    errors = {};
    addDialog.showModal();
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (adding) return;
    adding = true;
    errors = {};
    const s = $state.snapshot(spec);
    const own = {
      name: s.name.trim(),
      description: s.description.trim(),
      cpu_type: s.cpu_type,
      cores: Number(s.cores),
      memory: Number(s.memory),
      disk: Number(s.disk),
      locations: s.locations.split(",").map((l) => l.trim()).filter(Boolean),
    };
    try {
      const invalid = await service<Invalid>("Service", "AddServerType", own);
      if (invalid) {
        errors = { [invalid.field]: invalid.message };
      } else {
        addDialog.close();
        await load();
      }
    } catch (err) {
      showError("Add server type", err);
    } finally {
      adding = false;
    }
  }

  // Remove an own type.
  let removing = $state<Row | undefined>();
  let confirmOpen = $state(false);

  async function remove() {
    const name = removing!.name;
    await action(`Remove server type ${name}`, () => service<void>("Service", "RemoveServerType", name));
    await load();
  }

  // Update from Hetzner: the token is sent once and the field is cleared
  // after the update, whatever its result.
  let token = $state("");
  let updating = $state(false);

  async function update(e: SubmitEvent) {
    e.preventDefault();
    if (updating || !token) return;
    updating = true;
    const t = token;
    try {
      await action(
        "Update from Hetzner",
        () => mgmt<{ server_types: number }>("POST", "/catalog/update", { hetzner_token: t }),
        (r) => `Catalog updated: ${r.server_types} server types`,
      );
    } finally {
      token = "";
      updating = false;
    }
    await load();
  }
</script>

<div class="title-row">
  <h1>Server Types</h1>
  <HelpButton view="server-types" />
</div>

{#if problem}
  <div class="problem" role="alert">Own server types left out: {problem}</div>
{/if}

<form class="update" onsubmit={update}>
  <Field label="Hetzner token" hint={$running ? "Used once for the update, never stored." : "Start the cloud to update from Hetzner."}>
    {#snippet children(c)}
      <input
        id={c.id}
        type="password"
        autocomplete="off"
        required
        aria-describedby={c.describedby}
        disabled={!$running}
        bind:value={token}
      />
    {/snippet}
  </Field>
  <Button type="submit" variant="primary" loading={updating} disabled={!$running || !token}>Update from Hetzner</Button>
</form>

<Table
  title="Server types"
  {rows}
  {columns}
  key={(t) => `${t.source}-${t.id}`}
  name={(t) => t.name}
  emptyText="There are no server types."
  createLabel="Add server type"
  oncreate={openAdd}
  actions={(t) => (t.source === "own" ? [{ label: "Remove", onclick: () => ((removing = t), (confirmOpen = true)) }] : [])}
/>

<dialog bind:this={addDialog} aria-labelledby="add-type-title">
  <form onsubmit={add} novalidate>
    <h2 id="add-type-title">Add server type</h2>
    <Field label="Name" error={errors.name} hint="Must not match a catalog type">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.name} />
      {/snippet}
    </Field>
    <Field label="Description" error={errors.description}>
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.description} />
      {/snippet}
    </Field>
    <div class="row">
      <Field label="Cores" error={errors.cores}>
        {#snippet children(c)}
          <input id={c.id} type="number" min="1" step="1" aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.cores} />
        {/snippet}
      </Field>
      <Field label="Memory (GB)" error={errors.memory}>
        {#snippet children(c)}
          <input id={c.id} type="number" min="0" step="0.5" aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.memory} />
        {/snippet}
      </Field>
      <Field label="Disk (GB)" error={errors.disk}>
        {#snippet children(c)}
          <input id={c.id} type="number" min="1" step="1" aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.disk} />
        {/snippet}
      </Field>
    </div>
    <Field label="CPU type" error={errors.cpu_type}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.cpu_type}>
          <option value="shared">shared</option>
          <option value="dedicated">dedicated</option>
        </select>
      {/snippet}
    </Field>
    <Field label="Locations" error={errors.locations} hint="Comma-separated location names; empty means all">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.locations} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => addDialog.close()} disabled={adding}>Cancel</Button>
      <Button type="submit" variant="primary" loading={adding}>Add</Button>
    </div>
  </form>
</dialog>

<ConfirmDialog bind:open={confirmOpen} title="Remove server type" confirmLabel="Remove" onconfirm={remove}>
  <p>Removing <strong>{removing?.name}</strong> keeps existing servers of this type running; new servers cannot use it.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  .problem {
    margin-bottom: var(--s4);
    padding: var(--s3);
    border: 1px solid var(--error);
    border-radius: var(--radius);
    color: var(--error);
  }
  .update {
    display: flex;
    align-items: flex-start;
    gap: var(--s3);
    margin-bottom: var(--s6);
  }
  .update :global(.field) {
    width: 320px;
  }
  .update :global(.btn) {
    margin-top: 22px;
  }
  dialog {
    width: 480px;
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
  input,
  select {
    width: 100%;
  }
  .row {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: var(--s3);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
</style>
