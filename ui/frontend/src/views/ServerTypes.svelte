<script lang="ts">
  import Button from "../lib/Button.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Dialog from "../lib/Dialog.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { mgmt, service } from "../lib/bridge";
  import { actionTitle } from "../lib/format";
  import { action, errorText, showError, showSuccess } from "../lib/toast";
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

  // Add and edit dialog; the field names are those of catalog.OwnSpec.
  // "All locations" saves no locations, which means all. While editing, the
  // name of the own type is fixed.
  type Location = { name: string; city: string };
  let addOpen = $state(false);
  let locations = $state<Location[]>([]);
  const blank = () => ({
    name: "",
    description: "",
    cores: 2,
    memory: 4,
    disk: 40,
    cpu_type: "shared",
    all: true,
    locations: [] as string[],
  });
  let spec = $state(blank());
  let errors = $state<Record<string, string>>({});
  let adding = $state(false);
  let editing = $state(false);

  async function loadLocations() {
    try {
      locations = await service<Location[]>("Service", "Locations");
    } catch (err) {
      showError("Load locations", err);
    }
  }

  async function openAdd() {
    spec = blank();
    errors = {};
    editing = false;
    addOpen = true;
    await loadLocations();
  }

  async function openEdit(t: Row) {
    await loadLocations();
    const own = (t.locations ?? []).map((l) => l.name);
    const all = locations.length > 0 && locations.every((l) => own.includes(l.name));
    spec = {
      name: t.name,
      description: t.description ?? "",
      cores: t.cores,
      memory: t.memory,
      disk: t.disk,
      cpu_type: t.cpu_type,
      all,
      locations: all ? [] : own,
    };
    errors = {};
    editing = true;
    addOpen = true;
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (adding) return;
    errors = {};
    const s = $state.snapshot(spec);
    if (!s.all && s.locations.length === 0) {
      errors = { locations: "Choose at least one location or All locations." };
      return;
    }
    adding = true;
    const own = {
      name: s.name.trim(),
      description: s.description.trim(),
      cpu_type: s.cpu_type,
      cores: Number(s.cores),
      memory: Number(s.memory),
      disk: Number(s.disk),
      locations: s.all ? [] : s.locations,
    };
    try {
      const invalid = await service<Invalid>("Service", editing ? "UpdateServerType" : "AddServerType", own);
      if (invalid) {
        errors = { [invalid.field]: invalid.message };
      } else {
        addOpen = false;
        await load();
      }
    } catch (err) {
      showError(actionTitle("Server type", own.name, editing ? "Edit" : "Add"), err);
    } finally {
      adding = false;
    }
  }

  // Remove an own type.
  let removing = $state<Row | undefined>();
  let confirmOpen = $state(false);

  async function remove() {
    const name = removing!.name;
    await action(actionTitle("Server type", name, "Remove"), () => service<void>("Service", "RemoveServerType", name));
    await load();
  }

  // Update from Hetzner: the token is sent once; it is cleared whenever the
  // dialog closes and never stored. An error stays under the field.
  let updateOpen = $state(false);
  let token = $state("");
  let tokenError = $state("");
  let updating = $state(false);

  function openUpdate() {
    token = "";
    tokenError = "";
    updateOpen = true;
  }

  function updateClosed() {
    token = "";
    tokenError = "";
  }

  async function update(e: SubmitEvent) {
    e.preventDefault();
    if (updating) return;
    updating = true;
    tokenError = "";
    try {
      const r = await mgmt<{ server_types: number }>("POST", "/catalog/update", { hetzner_token: token });
      updateOpen = false;
      showSuccess("Update from Hetzner", `Catalog updated: ${r.server_types} server types`);
      await load();
    } catch (err) {
      tokenError = errorText(err);
    } finally {
      updating = false;
    }
  }
</script>

<div class="title-row">
  <h1>Server Types</h1>
  <HelpButton view="server-types" />
  <span class="spacer"></span>
  <Button
    variant="primary"
    disabled={!$running}
    title={$running ? undefined : "Start the cloud to update from Hetzner."}
    onclick={openUpdate}>Update from Hetzner</Button
  >
</div>

{#if problem}
  <div class="problem" role="alert">Own server types left out: {problem}</div>
{/if}

<Table
  title="Server types"
  {rows}
  {columns}
  key={(t) => `${t.source}-${t.id}`}
  name={(t) => t.name}
  emptyText="There are no server types."
  createLabel="Add server type"
  oncreate={openAdd}
  actions={(t) =>
    t.source === "own"
      ? [
          { label: "Edit", onclick: () => openEdit(t) },
          { label: "Remove", onclick: () => ((removing = t), (confirmOpen = true)) },
        ]
      : []}
/>

<Dialog bind:open={addOpen} title={editing ? "Edit server type" : "Add server type"}>
  <form onsubmit={add} novalidate>
    <Field label="Name" error={errors.name} hint={editing ? undefined : "Must not match a catalog type"}>
      {#snippet children(c)}
        <input
          id={c.id}
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          autocomplete="off"
          readonly={editing}
          bind:value={spec.name}
        />
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
    <fieldset class="locations" aria-describedby={errors.locations ? "locations-error" : undefined}>
      <legend>Locations</legend>
      <label class="check"><input type="checkbox" bind:checked={spec.all} /> All locations</label>
      {#each locations as l (l.name)}
        <label class="check">
          <input type="checkbox" value={l.name} disabled={spec.all} bind:group={spec.locations} />
          {l.name} ({l.city})
        </label>
      {/each}
      {#if errors.locations}<div id="locations-error" class="error">{errors.locations}</div>{/if}
    </fieldset>
    <div class="actions">
      <Button onclick={() => (addOpen = false)} disabled={adding}>Cancel</Button>
      <Button type="submit" variant="primary" loading={adding}>{editing ? "Save" : "Add"}</Button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={updateOpen} title="Update from Hetzner" onclose={updateClosed}>
  <form onsubmit={update}>
    <Field label="Hetzner token" error={tokenError} hint="Used once for the update, never stored.">
      {#snippet children(c)}
        <input
          id={c.id}
          type="password"
          autocomplete="off"
          required
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          bind:value={token}
        />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (updateOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" loading={updating}>Update</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog bind:open={confirmOpen} title="Remove server type" confirmLabel="Remove" typedName={removing?.name} onconfirm={remove}>
  <p>Removing <strong>{removing?.name}</strong> keeps existing servers of this type running; new servers cannot use it.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  .spacer {
    flex: 1;
  }
  .problem {
    margin-bottom: var(--s4);
    padding: var(--s3);
    border: 1px solid var(--error);
    border-radius: var(--radius);
    color: var(--error);
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
  .locations {
    display: flex;
    flex-direction: column;
    gap: var(--s1);
    margin: 0 0 var(--s4);
    padding: 0;
    border: 0;
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
  .check input {
    width: auto;
  }
  .error {
    font-size: 12px;
    line-height: 16px;
    color: var(--error);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
</style>
