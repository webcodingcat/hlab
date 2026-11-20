<script module lang="ts">
  /** Network as the Hetzner API returns it (the fields the views use). */
  export type Network = {
    id: number;
    name: string;
    ip_range: string;
    subnets: { type: string; ip_range: string; network_zone: string; gateway: string }[];
    routes: { destination: string; gateway: string }[];
    servers: number[];
    load_balancers: number[];
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
  import { listAll, runOn } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/networks";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import NetworkCreate from "./NetworkCreate.svelte";
  import NetworkDetail from "./NetworkDetail.svelte";

  // The "Networks" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "networks", page: { kind: "list" } }));

  let rows = $state<Network[] | undefined>();
  let failing = false;

  const columns: Column<Network>[] = [
    { key: "name", label: "Name", value: (n) => n.name, sortable: true },
    { key: "ip_range", label: "IP range", value: (n) => n.ip_range, sortable: true },
    { key: "subnets", label: "Subnets", value: (n) => n.subnets.map((s) => s.ip_range).join(", ") },
    { key: "servers", label: "Servers", value: (n) => n.servers.length, sortable: true },
    { key: "labels", label: "Labels", value: (n) => labelText(n.labels) },
    { key: "created", label: "Created", value: (n) => createdText(n.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Network>(project, "/networks", "networks");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load networks", err);
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
    location.hash = href({ area: "project", project, view: "networks", page: p });
  }

  // Row menu: Delete with the name typed.
  let pending = $state<Network | undefined>();
  let confirmOpen = $state(false);

  function ask(n: Network) {
    pending = n;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const n = pending;
    const req = request(tabs.delete, n.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, n.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Networks</h1>
    <HelpButton view="networks" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <NetworkCreate {project} {list} />
{:else if page.kind === "detail"}
  <NetworkDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Networks</h1>
    <HelpButton view="networks" />
  </div>
  <Table
    title="Networks"
    {rows}
    {columns}
    key={(n) => n.id}
    name={(n) => n.name}
    labels={(n) => n.labels}
    emptyText="This project has no networks yet."
    createLabel="Create network"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(n) => open({ kind: "detail", id: n.id, tab: "overview" })}
    actions={(n) => [{ label: "Delete", onclick: () => ask(n) }]}
  />
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title="Delete network"
  confirmLabel="Delete"
  typedName={pending?.name}
  onconfirm={run}
>
  {#if pending}
    <p>
      The network <strong>{pending.name}</strong> is deleted for good; attached servers and load balancers lose their
      private IPs in it.
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
