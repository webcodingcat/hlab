<script module lang="ts">
  /** Zone as the Hetzner API returns it (the fields the views use). */
  export type Zone = {
    id: number;
    name: string;
    mode: "primary" | "secondary";
    ttl: number;
    record_count: number;
    primary_nameservers: { address: string; port: number; tsig_algorithm?: string; tsig_key?: string }[];
    authoritative_nameservers: { assigned: string[] };
    protection: { delete: boolean };
    labels: Record<string, string>;
    created: string;
  };

  /** modeText shows the mode as Primary or Secondary. */
  export const modeText = (m: string) => (m === "secondary" ? "Secondary" : "Primary");
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runApi } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/zones";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import ZoneCreate from "./ZoneCreate.svelte";
  import ZoneDetail from "./ZoneDetail.svelte";

  // The "DNS" section of a project console; page says which of its pages
  // the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "dns", page: { kind: "list" } }));

  let rows = $state<Zone[] | undefined>();
  let failing = false;

  const columns: Column<Zone>[] = [
    { key: "name", label: "Name", value: (z) => z.name, sortable: true },
    { key: "mode", label: "Mode", value: (z) => modeText(z.mode), sortable: true },
    { key: "records", label: "Records", value: (z) => z.record_count, sortable: true },
    { key: "ttl", label: "TTL", value: (z) => z.ttl, sortable: true },
    { key: "labels", label: "Labels", value: (z) => labelText(z.labels) },
    { key: "created", label: "Created", value: (z) => createdText(z.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Zone>(project, "/zones", "zones");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load zones", err);
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
    location.hash = href({ area: "project", project, view: "dns", page: p });
  }

  // Row menu: Delete with the name typed.
  let pending = $state<Zone | undefined>();
  let confirmOpen = $state(false);

  function ask(z: Zone) {
    pending = z;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const z = pending;
    const req = request(tabs.delete, z.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runApi(project, `${tabs.delete.label} ${z.name}`, req.method, req.path, req.body).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>DNS</h1>
    <HelpButton view="dns" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <ZoneCreate {project} {list} />
{:else if page.kind === "detail"}
  <ZoneDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>DNS</h1>
    <HelpButton view="dns" />
  </div>
  <Table
    title="DNS zones"
    {rows}
    {columns}
    key={(z) => z.id}
    name={(z) => z.name}
    labels={(z) => z.labels}
    emptyText="This project has no DNS zones yet."
    createLabel="Add zone"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(z) => open({ kind: "detail", id: z.id, tab: "overview" })}
    actions={(z) => [{ label: "Delete", onclick: () => ask(z) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete zone" confirmLabel="Delete" typedName={pending?.name} onconfirm={run}>
  {#if pending}
    <p>The zone <strong>{pending.name}</strong> is deleted for good with all its records.</p>
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
