<script module lang="ts">
  /** Image is a snapshot or backup as the Hetzner API returns it (the fields the views use). */
  export type Image = {
    id: number;
    type: string;
    status: string;
    description: string;
    image_size: number | null;
    disk_size: number;
    created: string;
    created_from: { id: number; name: string } | null;
    protection: { delete: boolean };
    labels: Record<string, string>;
  };

  /** imageName is how the views name an image: its description, or its type and ID. */
  export function imageName(i: Image): string {
    return i.description || `${i.type} ${i.id}`;
  }

  /** sizeText is the size of an image's disk file, "-" while it is unknown. */
  export function sizeText(i: Image): string {
    return i.image_size === null ? "-" : `${i.image_size.toFixed(2)} GB`;
  }
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runApi } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/images";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import ImageDetail from "./ImageDetail.svelte";

  // The "Images" section of a project console: the project's snapshots and
  // backups (no system images); page says whether the list or a detail page
  // shows. Images are made in a server's Backups and Snapshots tabs, so
  // there is no create form.
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "images", page: { kind: "list" } }));

  let rows = $state<Image[] | undefined>();
  let failing = false;

  const columns: Column<Image>[] = [
    { key: "name", label: "Name", value: imageName, sortable: true },
    { key: "type", label: "Type", value: (i) => i.type, sortable: true },
    { key: "size", label: "Size", value: sizeText, sortable: true },
    { key: "created_from", label: "Created from", value: (i) => i.created_from?.name ?? "-", sortable: true },
    { key: "labels", label: "Labels", value: (i) => labelText(i.labels) },
    { key: "created", label: "Created", value: (i) => createdText(i.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Image>(project, "/images?type=snapshot&type=backup", "images");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load images", err);
      failing = true;
    }
  }

  // The list refreshes every 5 s while it is shown.
  $effect(() => {
    if (!$running || page.kind === "detail") return;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function open(p: Page) {
    location.hash = href({ area: "project", project, view: "images", page: p });
  }

  // Row menu: Delete after a plain confirmation.
  let pending = $state<Image | undefined>();
  let confirmOpen = $state(false);

  function ask(i: Image) {
    pending = i;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const i = pending;
    const req = request(tabs.delete, i.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runApi(project, `${tabs.delete.label} ${imageName(i)}`, req.method, req.path, req.body).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Images</h1>
    <HelpButton view="images" />
  </div>
  <CloudStopped />
{:else if page.kind === "detail"}
  <ImageDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Images</h1>
    <HelpButton view="images" />
  </div>
  <Table
    title="Images"
    {rows}
    {columns}
    key={(i) => i.id}
    name={imageName}
    labels={(i) => i.labels}
    emptyText="This project has no snapshots or backups yet. They are made in a server's Backups and Snapshots tabs."
    onrowclick={(i) => open({ kind: "detail", id: i.id, tab: "overview" })}
    actions={(i) => [{ label: "Delete", onclick: () => ask(i) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title={`Delete ${pending?.type ?? "image"}`} confirmLabel="Delete" onconfirm={run}>
  {#if pending}
    <p>The {pending.type} <strong>{imageName(pending)}</strong> is deleted for good.</p>
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
