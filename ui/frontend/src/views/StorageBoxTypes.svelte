<script lang="ts">
  import Button from "../lib/Button.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Dialog from "../lib/Dialog.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { service } from "../lib/bridge";
  import { actionTitle } from "../lib/format";
  import { action, showError } from "../lib/toast";

  // The list comes from the bridge, which reads the catalog and the own
  // types file directly, so it shows while the cloud is stopped too.
  type BoxType = {
    id: number;
    name: string;
    description: string;
    size: number;
    snapshot_limit: number;
    automatic_snapshot_limit: number;
    subaccounts_limit: number;
    prices?: { location: string }[] | null;
  };
  // OwnSpec has the field names of catalog.OwnStorageBoxTypeSpec.
  type OwnSpec = {
    name: string;
    description: string;
    size: number;
    snapshot_limit: number;
    automatic_snapshot_limit: number;
    subaccounts_limit: number;
    locations: string[];
  };
  type Row = BoxType & { source: "catalog" | "own"; own?: OwnSpec };
  type TypeList = { types: { type: BoxType; source: "catalog" | "own"; own?: OwnSpec }[]; problem?: string };
  type Invalid = { field: string; message: string } | null;

  const units = { MiB: 2 ** 20, GiB: 2 ** 30, TiB: 2 ** 40 } as const;
  type Unit = keyof typeof units;

  // sizeText shows bytes in the largest unit that fits.
  function sizeText(bytes: number): string {
    for (const u of ["TiB", "GiB", "MiB"] as Unit[]) {
      if (bytes >= units[u]) return `${+(bytes / units[u]).toFixed(2)} ${u}`;
    }
    return `${bytes} B`;
  }

  let rows = $state<Row[] | undefined>();
  let problem = $state("");
  let failing = false;

  const columns: Column<Row>[] = [
    { key: "name", label: "Name", value: (t) => t.name, sortable: true },
    { key: "description", label: "Description", value: (t) => t.description ?? "" },
    { key: "size", label: "Size", value: (t) => sizeText(t.size), sortable: true },
    { key: "snapshot_limit", label: "Snapshot limit", value: (t) => t.snapshot_limit, sortable: true },
    { key: "automatic_snapshot_limit", label: "Automatic snapshot limit", value: (t) => t.automatic_snapshot_limit, sortable: true },
    { key: "subaccounts_limit", label: "Subaccounts limit", value: (t) => t.subaccounts_limit, sortable: true },
    { key: "locations", label: "Locations", value: (t) => (t.prices ?? []).map((p) => p.location).join(", ") },
    { key: "source", label: "Source", value: (t) => t.source, sortable: true },
  ];

  async function load() {
    try {
      const list = await service<TypeList>("Service", "StorageBoxTypes");
      rows = list.types.map((t) => ({ ...t.type, source: t.source, own: t.own }));
      problem = list.problem ?? "";
      failing = false;
    } catch (err) {
      if (!failing) showError("Load storage box types", err);
      failing = true;
    }
  }

  // Lists refresh every 5 seconds while visible.
  $effect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Add and edit dialog. "All locations" sends every catalog location
  // explicitly; without it at least one location is required. While
  // editing, the name of the own type is fixed.
  type Location = { name: string; city: string };
  let addOpen = $state(false);
  let locations = $state<Location[]>([]);
  const blank = () => ({
    name: "",
    description: "",
    size: 1 as number,
    unit: "GiB" as Unit,
    snapshot_limit: 10,
    automatic_snapshot_limit: 10,
    subaccounts_limit: 100,
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

  // splitSize gives the largest unit that holds bytes as a whole number.
  function splitSize(bytes: number): { size: number; unit: Unit } {
    for (const u of ["TiB", "GiB"] as Unit[]) {
      if (bytes >= units[u] && bytes % units[u] === 0) return { size: bytes / units[u], unit: u };
    }
    return { size: bytes / units.MiB, unit: "MiB" };
  }

  async function openEdit(t: Row) {
    await loadLocations();
    const own = t.own!.locations;
    const all = locations.length > 0 && locations.every((l) => own.includes(l.name));
    spec = {
      name: t.name,
      description: t.description ?? "",
      ...splitSize(t.own!.size),
      snapshot_limit: t.snapshot_limit,
      automatic_snapshot_limit: t.automatic_snapshot_limit,
      subaccounts_limit: t.subaccounts_limit,
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
    const own: OwnSpec = {
      name: s.name.trim(),
      description: s.description.trim(),
      size: Math.round(Number(s.size) * units[s.unit]),
      snapshot_limit: Number(s.snapshot_limit),
      automatic_snapshot_limit: Number(s.automatic_snapshot_limit),
      subaccounts_limit: Number(s.subaccounts_limit),
      locations: s.all ? locations.map((l) => l.name) : s.locations,
    };
    try {
      const invalid = await service<Invalid>("Service", editing ? "UpdateStorageBoxType" : "AddStorageBoxType", own);
      if (invalid) {
        errors = { [invalid.field]: invalid.message };
      } else {
        addOpen = false;
        await load();
      }
    } catch (err) {
      showError(actionTitle("Storage box type", own.name, editing ? "Edit" : "Add"), err);
    } finally {
      adding = false;
    }
  }

  // Remove an own type.
  let removing = $state<Row | undefined>();
  let confirmOpen = $state(false);

  async function remove() {
    const name = removing!.name;
    await action(actionTitle("Storage box type", name, "Remove"), () => service<void>("Service", "RemoveStorageBoxType", name));
    await load();
  }
</script>

<div class="title-row">
  <h1>Storage Box Types</h1>
  <HelpButton view="storage-box-types" />
</div>

{#if problem}
  <div class="problem" role="alert">Own storage box types left out: {problem}</div>
{/if}

<Table
  title="Storage box types"
  {rows}
  {columns}
  key={(t) => `${t.source}-${t.id}-${t.name}`}
  name={(t) => t.name}
  emptyText="There are no storage box types."
  createLabel="Add storage box type"
  oncreate={openAdd}
  actions={(t) =>
    t.source === "own"
      ? [
          { label: "Edit", onclick: () => openEdit(t) },
          { label: "Remove", onclick: () => ((removing = t), (confirmOpen = true)) },
        ]
      : []}
/>

<Dialog bind:open={addOpen} title={editing ? "Edit storage box type" : "Add storage box type"}>
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
    <div class="row two">
      <Field label="Size" error={errors.size}>
        {#snippet children(c)}
          <input id={c.id} type="number" min="1" step="1" aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.size} />
        {/snippet}
      </Field>
      <Field label="Unit">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={spec.unit}>
            <option value="MiB">MiB</option>
            <option value="GiB">GiB</option>
            <option value="TiB">TiB</option>
          </select>
        {/snippet}
      </Field>
    </div>
    <div class="row">
      <Field label="Snapshot limit" error={errors.snapshot_limit}>
        {#snippet children(c)}
          <input
            id={c.id}
            type="number"
            min="0"
            step="1"
            aria-describedby={c.describedby}
            aria-invalid={c.invalid}
            bind:value={spec.snapshot_limit}
          />
        {/snippet}
      </Field>
      <Field label="Automatic snapshot limit" error={errors.automatic_snapshot_limit}>
        {#snippet children(c)}
          <input
            id={c.id}
            type="number"
            min="0"
            step="1"
            aria-describedby={c.describedby}
            aria-invalid={c.invalid}
            bind:value={spec.automatic_snapshot_limit}
          />
        {/snippet}
      </Field>
      <Field label="Subaccounts limit" error={errors.subaccounts_limit}>
        {#snippet children(c)}
          <input
            id={c.id}
            type="number"
            min="0"
            step="1"
            aria-describedby={c.describedby}
            aria-invalid={c.invalid}
            bind:value={spec.subaccounts_limit}
          />
        {/snippet}
      </Field>
    </div>
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

<ConfirmDialog bind:open={confirmOpen} title="Remove storage box type" confirmLabel="Remove" onconfirm={remove}>
  <p>Removing <strong>{removing?.name}</strong> keeps existing storage boxes of this type; new storage boxes cannot use it.</p>
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
  input,
  select {
    width: 100%;
  }
  .row {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: var(--s3);
  }
  .row.two {
    grid-template-columns: 2fr 1fr;
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
