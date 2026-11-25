<script module lang="ts">
  /** PlacementGroup as the Hetzner API returns it (the fields the views use). */
  export type PlacementGroup = {
    id: number;
    name: string;
    type: string;
    servers: number[];
    labels: Record<string, string>;
    created: string;
  };
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/placementGroups";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import PlacementGroupCreate from "./PlacementGroupCreate.svelte";
  import PlacementGroupDetail from "./PlacementGroupDetail.svelte";

  // The "Placement Groups" section of a project console; page says which of
  // its pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "placement-groups", page: { kind: "list" } }));

  let rows = $state<PlacementGroup[] | undefined>();
  let failing = false;

  const columns: Column<PlacementGroup>[] = [
    { key: "name", label: "Name", value: (g) => g.name, sortable: true },
    { key: "type", label: "Type", value: (g) => g.type, sortable: true },
    { key: "servers", label: "Servers", value: (g) => `${g.servers.length}`, sortable: true },
    { key: "labels", label: "Labels", value: (g) => labelText(g.labels) },
    { key: "created", label: "Created", value: (g) => createdText(g.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<PlacementGroup>(project, "/placement_groups", "placement_groups");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load placement groups", err);
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
    location.hash = href({ area: "project", project, view: "placement-groups", page: p });
  }

  // Row menu: Delete after a plain confirmation.
  let pending = $state<PlacementGroup | undefined>();
  let confirmOpen = $state(false);

  function ask(g: PlacementGroup) {
    pending = g;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const g = pending;
    const req = request(tabs.delete, g.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, g.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Placement Groups</h1>
    <HelpButton view="placement-groups" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <PlacementGroupCreate {project} {list} />
{:else if page.kind === "detail"}
  <PlacementGroupDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Placement Groups</h1>
    <HelpButton view="placement-groups" />
  </div>
  <Table
    title="Placement Groups"
    {rows}
    {columns}
    key={(g) => g.id}
    name={(g) => g.name}
    labels={(g) => g.labels}
    emptyText="This project has no placement groups yet."
    createLabel="Create placement group"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(g) => open({ kind: "detail", id: g.id, tab: "overview" })}
    actions={(g) => [{ label: "Delete", onclick: () => ask(g) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete placement group" confirmLabel="Delete" typedName={pending?.name} onconfirm={run}>
  {#if pending}
    <p>
      The placement group <strong>{pending.name}</strong> is deleted for good; its servers keep running outside any
      group.
    </p>
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
