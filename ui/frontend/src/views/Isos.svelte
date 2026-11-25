<script lang="ts">
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Dialog from "../lib/Dialog.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { service } from "../lib/bridge";
  import { actionTitle, isoTitle } from "../lib/format";
  import { action, showError } from "../lib/toast";
  import IsoForm, { type IsoSpec } from "./IsoForm.svelte";

  // The list comes from the bridge, which reads the catalog and the own ISOs
  // file, so it shows while the cloud is stopped too.
  type Iso = { id: number; name: string | null; description: string; architecture: string };
  type OwnSpec = {
    id: number;
    description: string;
    architecture: "x86" | "arm";
    source: { url?: string; path?: string; sha256?: string };
  };
  type Row = Iso & { source: "catalog" | "own"; own?: OwnSpec };
  type IsoList = { isos: { iso: Iso; source: "catalog" | "own"; own?: OwnSpec }[]; problem?: string };
  type Invalid = { field: string; message: string } | null;

  let rows = $state<Row[] | undefined>();
  let problem = $state("");
  let failing = false;

  const columns: Column<Row>[] = [
    { key: "source", label: "Source", value: (r) => r.source, sortable: true },
    { key: "name", label: "Name", value: (r) => isoTitle(r), sortable: true },
    { key: "description", label: "Description", value: (r) => r.description, sortable: true },
    { key: "architecture", label: "Architecture", value: (r) => r.architecture, sortable: true },
  ];

  async function load() {
    try {
      const list = await service<IsoList>("Service", "ISOs");
      rows = list.isos.map((l) => ({ ...l.iso, source: l.source, own: l.own }));
      problem = list.problem ?? "";
      failing = false;
    } catch (err) {
      if (!failing) showError("Load ISOs", err);
      failing = true;
    }
  }

  // Lists refresh every 5 seconds while visible.
  $effect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Add and edit dialog: the form state becomes a catalog.OwnISOSpec with
  // exactly one source.
  const blank = (): IsoSpec => ({ id: 0, name: "", architecture: "x86", source: "url", url: "", sha256: "", path: "" });
  let formOpen = $state(false);
  let spec = $state<IsoSpec>(blank());
  let errors = $state<Record<string, string>>({});
  let saving = $state(false);

  function openAdd() {
    spec = blank();
    errors = {};
    formOpen = true;
  }

  function openEdit(r: Row) {
    const o = r.own!;
    spec = {
      id: o.id,
      name: o.description,
      architecture: o.architecture,
      source: o.source.path ? "path" : "url",
      url: o.source.url ?? "",
      sha256: o.source.sha256 ?? "",
      path: o.source.path ?? "",
    };
    errors = {};
    formOpen = true;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (saving) return;
    errors = {};
    const s = $state.snapshot(spec);
    const own: OwnSpec = {
      id: s.id,
      description: s.name.trim(),
      architecture: s.architecture,
      source: s.source === "url" ? { url: s.url.trim(), sha256: s.sha256.trim() } : { path: s.path.trim() },
    };
    saving = true;
    try {
      const invalid = await service<Invalid>("Service", s.id ? "UpdateISO" : "AddISO", own);
      if (invalid) {
        // "No or both sources" names url or path; show it under the shown one.
        const field = invalid.field === "url" || invalid.field === "path" ? s.source : invalid.field;
        errors = { [field]: invalid.message };
      } else {
        formOpen = false;
        await load();
      }
    } catch (err) {
      showError(actionTitle("ISO", own.description, s.id ? "Edit" : "Add"), err);
    } finally {
      saving = false;
    }
  }

  // Remove an own ISO.
  let removing = $state<Row | undefined>();
  let confirmOpen = $state(false);

  async function remove() {
    const r = removing!;
    await action(actionTitle("ISO", isoTitle(r), "Remove"), () => service<void>("Service", "RemoveISO", r.id));
    await load();
  }
</script>

<div class="title-row">
  <h1>ISOs</h1>
  <HelpButton view="isos" />
</div>

{#if problem}
  <div class="problem" role="alert">Own ISOs left out: {problem}</div>
{/if}

<Table
  title="ISOs"
  {rows}
  {columns}
  key={(r) => `${r.source}-${r.id}`}
  name={(r) => isoTitle(r)}
  emptyText="There are no ISOs."
  createLabel="Add ISO"
  oncreate={openAdd}
  actions={(r) =>
    r.own
      ? [
          { label: "Edit", onclick: () => openEdit(r) },
          { label: "Remove", onclick: () => ((removing = r), (confirmOpen = true)) },
        ]
      : []}
/>

<Dialog bind:open={formOpen} title={spec.id ? "Edit ISO" : "Add ISO"}>
  <IsoForm bind:spec {errors} {saving} onsubmit={save} oncancel={() => (formOpen = false)} />
</Dialog>

<ConfirmDialog bind:open={confirmOpen} title="Remove ISO" confirmLabel="Remove" typedName={removing ? isoTitle(removing) : undefined} onconfirm={remove}>
  <p>
    Removing <strong>{removing ? isoTitle(removing) : ""}</strong> keeps it in servers that have it attached until it is
    detached; it can no longer be attached.
  </p>
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
</style>
