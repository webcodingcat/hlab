<script module lang="ts">
  /** bytesText shows a byte count in the largest binary unit that keeps it at 1 or more. */
  export function bytesText(n: number): string {
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
  }
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import StatusPill from "../../lib/StatusPill.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { hetznerListAll, runHetznerOn } from "../../lib/api";
  import { request } from "../../lib/resourceActions";
  import { tabs, type StorageBox } from "../../lib/resources/storageBoxes";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import Detail from "./Detail.svelte";
  import StorageBoxCreate from "./StorageBoxCreate.svelte";

  // The "Storage Boxes" section of a project console; page says which of
  // its pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "storage-boxes", page: { kind: "list" } }));

  let rows = $state<StorageBox[] | undefined>();
  let failing = false;

  const usageText = (b: StorageBox) =>
    `${bytesText(b.stats.size_data + b.stats.size_snapshots)} of ${bytesText(b.stats.size)}`;

  const columns: Column<StorageBox>[] = [
    { key: "name", label: "Name", value: (b) => b.name, sortable: true },
    { key: "username", label: "Username", value: (b) => b.username ?? "", sortable: true },
    { key: "type", label: "Type", value: (b) => b.storage_box_type.name, sortable: true },
    { key: "location", label: "Location", value: (b) => b.location.name, sortable: true },
    { key: "usage", label: "Usage", value: usageText },
    { key: "status", label: "Status", value: (b) => b.status, sortable: true },
  ];

  const tone = (status: string) => (status === "active" ? "success" : status === "initializing" ? "warning" : "muted");

  async function load() {
    try {
      rows = await hetznerListAll<StorageBox>(project, "/storage_boxes", "storage_boxes");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load storage boxes", err);
      failing = true;
    }
  }

  // The list refreshes every 5 s while it is shown.
  $effect(() => {
    if (!$running || page.kind !== "list") return;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function open(p: Page) {
    location.hash = href({ area: "project", project, view: "storage-boxes", page: p });
  }

  // Row menu: Delete with the name typed, disabled under delete protection.
  let pending = $state<StorageBox | undefined>();
  let confirmOpen = $state(false);

  function ask(box: StorageBox) {
    pending = box;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const box = pending;
    const req = request(tabs.delete, box.id);
    if (!req) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runHetznerOn(project, box.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Storage Boxes</h1>
    <HelpButton view="storage-boxes" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <StorageBoxCreate {project} {list} />
{:else if page.kind === "detail"}
  <Detail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Storage Boxes</h1>
    <HelpButton view="storage-boxes" />
  </div>
  <Table
    title="Storage Boxes"
    {rows}
    {columns}
    key={(b) => b.id}
    name={(b) => b.name}
    labels={(b) => b.labels}
    emptyText="This project has no storage boxes yet."
    createLabel="Create Storage Box"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(b) => open({ kind: "detail", id: b.id, tab: "overview" })}
    actions={(b) => [
      {
        label: "Delete",
        onclick: () => ask(b),
        disabled: b.protection.delete,
        hint: b.protection.delete ? "Delete protection is on; turn it off in Settings first." : undefined,
      },
    ]}
  >
    {#snippet cell(b, c)}
      {#if c.key === "status"}<StatusPill tone={tone(b.status)} text={b.status} />{:else}{c.value(b)}{/if}
    {/snippet}
  </Table>
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title="Delete storage box"
  confirmLabel="Delete"
  typedName={pending?.name}
  onconfirm={run}
>
  {#if pending}
    <p>The storage box <strong>{pending.name}</strong>, its subaccounts, snapshots and data are deleted for good.</p>
  {/if}
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
</style>
