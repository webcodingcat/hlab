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
  import Dialog from "../../lib/Dialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { rowAction, tabs } from "../../lib/resources/volumes";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import VolumeActionInput from "./VolumeActionInput.svelte";
  import VolumeCreate from "./VolumeCreate.svelte";
  import VolumeDetail from "./VolumeDetail.svelte";

  // The "Volumes" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "volumes", page: { kind: "list" } }));

  let rows = $state<Volume[] | undefined>();
  type Server = { id: number; name: string; location: { name: string } };
  let servers = $state<Server[]>([]);
  let failing = false;

  const serverText = (v: Volume) => (v.server === null ? "" : (servers.find((s) => s.id === v.server)?.name ?? `${v.server}`));

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
        listAll<Server>(project, "/servers", "servers"),
      ]);
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
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

  // Row menu: Attach (with the server select) while detached, Detach with a
  // plain confirmation while attached; Delete with the name typed.
  let pending = $state<{ volume: Volume; action: ResourceAction; input: Input } | undefined>();
  let inputOpen = $state(false);
  let confirmOpen = $state(false);

  function ask(volume: Volume, action: ResourceAction) {
    pending = { volume, action, input: {} };
    if (action.needsInput) inputOpen = true;
    else confirmOpen = true;
  }

  function submit(input: Input) {
    if (!pending) return;
    inputOpen = false;
    pending = { ...pending, input };
    if (pending.action.confirm === "none") run();
    else confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const { volume, action, input } = pending;
    const req = request(action, volume.id, input);
    if (!req) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, volume.name, action.label, req).then(load);
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
{:else if page.kind === "detail"}
  <VolumeDetail {project} id={page.id} tab={page.tab} {list} />
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
    onrowclick={(v) => open({ kind: "detail", id: v.id, tab: "overview" })}
    actions={(v) => [
      { label: rowAction(v).label, onclick: () => ask(v, rowAction(v)) },
      { label: "Delete", onclick: () => ask(v, tabs.delete) },
    ]}
  />
{/if}

<Dialog bind:open={inputOpen} title={`${pending?.action.label ?? ""} ${pending?.volume.name ?? ""}`}>
  {#if pending && inputOpen}
    <VolumeActionInput action={pending.action} volume={pending.volume} {servers} {submit} cancel={() => (inputOpen = false)} />
  {/if}
</Dialog>

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
