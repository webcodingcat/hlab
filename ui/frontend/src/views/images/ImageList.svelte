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
  import { actionTitle, createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/images";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import ImageDetail from "./ImageDetail.svelte";

  // The list of a project's images of one type, the body of the Snapshots
  // and the Backups section (system images are not listed); page says
  // whether the list or a detail page shows. Images are made in a server's
  // Backups and Snapshots tabs, so there is no create form.
  let { project, page, type }: { project: string; page: Page; type: "snapshot" | "backup" } = $props();

  const view = $derived(type === "snapshot" ? "snapshots" : "backups");
  const title = $derived(type === "snapshot" ? "Snapshots" : "Backups");
  const emptyText = $derived(
    type === "snapshot"
      ? "This project has no snapshots yet. They are made in a server's Snapshots tab."
      : "This project has no backups yet. They are made in a server's Backups tab.",
  );
  const list = $derived(href({ area: "project", project, view, page: { kind: "list" } }));

  let rows = $state<Image[] | undefined>();
  let failing = false;

  const columns: Column<Image>[] = [
    { key: "name", label: "Name", value: imageName, sortable: true },
    { key: "size", label: "Size", value: sizeText, sortable: true },
    { key: "created_from", label: "Created from", value: (i) => i.created_from?.name ?? "-", sortable: true },
    { key: "labels", label: "Labels", value: (i) => labelText(i.labels) },
    { key: "created", label: "Created", value: (i) => createdText(i.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Image>(project, `/images?type=${type}`, "images");
      failing = false;
    } catch (err) {
      if (!failing) showError(`Load ${view}`, err);
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
    location.hash = href({ area: "project", project, view, page: p });
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
    runApi(project, actionTitle(type === "snapshot" ? "Snapshot" : "Backup", imageName(i), tabs.delete.label), req.method, req.path, req.body).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>{title}</h1>
    <HelpButton {view} />
  </div>
  <CloudStopped />
{:else if page.kind === "detail"}
  <ImageDetail {project} {view} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>{title}</h1>
    <HelpButton {view} />
  </div>
  <Table
    {title}
    {rows}
    {columns}
    key={(i) => i.id}
    name={imageName}
    labels={(i) => i.labels}
    {emptyText}
    onrowclick={(i) => open({ kind: "detail", id: i.id, tab: "overview" })}
    actions={(i) => [{ label: "Delete", onclick: () => ask(i) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title={`Delete ${type}`} confirmLabel="Delete" onconfirm={run}>
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
