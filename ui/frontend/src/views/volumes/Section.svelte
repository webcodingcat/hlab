<script module lang="ts">
  /** Volume as the Hetzner API returns it. */
  export type Volume = {
    id: number;
    name: string;
    size: number;
    server: number | null;
    location: { name: string };
    linux_device: string;
    status: string;
    protection: { delete: boolean };
    labels: Record<string, string>;
    created: string;
  };
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runApi } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request, type ResourceAction } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/volumes";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import VolumeCreate from "./VolumeCreate.svelte";

  // The "Volumes" section of a project console; page says which of its
  // pages the route opens (list or create form).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "volumes", page: { kind: "list" } }));

  let rows = $state<Volume[] | undefined>();
  let serverNames = $state<Record<number, string>>({});
  let failing = false;

  const serverText = (v: Volume) => (v.server === null ? "" : (serverNames[v.server] ?? `${v.server}`));

  const columns: Column<Volume>[] = [
    { key: "name", label: "Name", value: (v) => v.name, sortable: true },
    { key: "size", label: "Size", value: (v) => `${v.size} GB`, sortable: true },
    { key: "server", label: "Server", value: serverText, sortable: true },
    { key: "location", label: "Location", value: (v) => v.location.name, sortable: true },
    { key: "labels", label: "Labels", value: (v) => labelText(v.labels) },
    { key: "created", label: "Created", value: (v) => createdText(v.created), sortable: true },
  ];

  async function load() {
    try {
      const [v, s] = await Promise.all([
        listAll<Volume>(project, "/volumes", "volumes"),
        listAll<{ id: number; name: string }>(project, "/servers", "servers"),
      ]);
      serverNames = Object.fromEntries(s.map((x) => [x.id, x.name]));
      rows = v;
      failing = false;
    } catch (err) {
      if (!failing) showError("Load volumes", err);
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
    location.hash = href({ area: "project", project, view: "volumes", page: p });
  }

  // Row menu: Detach with a plain confirmation, Delete with the name typed.
  let pending = $state<{ volume: Volume; action: ResourceAction } | undefined>();
  let confirmOpen = $state(false);

  function ask(volume: Volume, action: ResourceAction) {
    pending = { volume, action };
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const { volume, action } = pending;
    const req = request(action, volume.id);
    if (!req) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runApi(project, `${action.label} ${volume.name}`, req.method, req.path, req.body).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Volumes</h1>
    <HelpButton view="volumes" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <VolumeCreate {project} {list} />
{:else}
  <div class="title-row">
    <h1>Volumes</h1>
    <HelpButton view="volumes" />
  </div>
  <Table
    title="Volumes"
    {rows}
    {columns}
    key={(v) => v.id}
    name={(v) => v.name}
    labels={(v) => v.labels}
    emptyText="This project has no volumes yet."
    createLabel="Create volume"
    oncreate={() => open({ kind: "new" })}
    actions={(v) => [
      {
        label: "Detach",
        onclick: () => ask(v, tabs.detach),
        disabled: v.server === null,
        hint: v.server === null ? "The volume is not attached." : undefined,
      },
      { label: "Delete", onclick: () => ask(v, tabs.delete) },
    ]}
  />
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} volume`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? pending.volume.name : undefined}
  onconfirm={run}
>
  {#if pending?.action.id === "delete"}
    <p>The volume <strong>{pending.volume.name}</strong> and its data are deleted for good.</p>
  {:else if pending}
    <p>The volume <strong>{pending.volume.name}</strong> is detached from its server.</p>
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
