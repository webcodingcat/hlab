<script module lang="ts">
  /** FloatingIp as the Hetzner API returns it. */
  export type FloatingIp = {
    id: number;
    name: string;
    description: string | null;
    ip: string;
    type: "ipv4" | "ipv6";
    server: number | null;
    dns_ptr: { ip: string; dns_ptr: string }[];
    home_location: { name: string; network_zone: string };
    blocked: boolean;
    protection: { delete: boolean };
    labels: Record<string, string>;
    created: string;
  };

  /** protocolText shows the type as IPv4 or IPv6. */
  export const protocolText = (t: string) => (t === "ipv6" ? "IPv6" : "IPv4");
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runApi } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request, type ResourceAction } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/floatingIps";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import FloatingIpCreate from "./FloatingIpCreate.svelte";
  import FloatingIpDetail from "./FloatingIpDetail.svelte";

  // The "Floating IPs" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "floating-ips", page: { kind: "list" } }));

  let rows = $state<FloatingIp[] | undefined>();
  let serverNames = $state<Record<number, string>>({});
  let failing = false;

  const serverText = (f: FloatingIp) => (f.server === null ? "" : (serverNames[f.server] ?? `${f.server}`));

  const columns: Column<FloatingIp>[] = [
    { key: "name", label: "Name", value: (f) => f.name, sortable: true },
    { key: "ip", label: "IP", value: (f) => f.ip, sortable: true },
    { key: "protocol", label: "Protocol", value: (f) => protocolText(f.type), sortable: true },
    { key: "home_location", label: "Home location", value: (f) => f.home_location.name, sortable: true },
    { key: "server", label: "Server", value: serverText, sortable: true },
    { key: "labels", label: "Labels", value: (f) => labelText(f.labels) },
    { key: "created", label: "Created", value: (f) => createdText(f.created), sortable: true },
  ];

  async function load() {
    try {
      const [f, s] = await Promise.all([
        listAll<FloatingIp>(project, "/floating_ips", "floating_ips"),
        listAll<{ id: number; name: string }>(project, "/servers", "servers"),
      ]);
      serverNames = Object.fromEntries(s.map((x) => [x.id, x.name]));
      rows = f;
      failing = false;
    } catch (err) {
      if (!failing) showError("Load floating IPs", err);
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
    location.hash = href({ area: "project", project, view: "floating-ips", page: p });
  }

  // Row menu: Unassign and Delete, each with a plain confirmation.
  let pending = $state<{ fip: FloatingIp; action: ResourceAction } | undefined>();
  let confirmOpen = $state(false);

  function ask(fip: FloatingIp, action: ResourceAction) {
    pending = { fip, action };
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const { fip, action } = pending;
    const req = request(action, fip.id);
    if (!req) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runApi(project, `${action.label} ${fip.name}`, req.method, req.path, req.body).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Floating IPs</h1>
    <HelpButton view="floating-ips" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <FloatingIpCreate {project} {list} />
{:else if page.kind === "detail"}
  <FloatingIpDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Floating IPs</h1>
    <HelpButton view="floating-ips" />
  </div>
  <Table
    title="Floating IPs"
    {rows}
    {columns}
    key={(f) => f.id}
    name={(f) => f.name}
    labels={(f) => f.labels}
    emptyText="This project has no floating IPs yet."
    createLabel="Create floating IP"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(f) => open({ kind: "detail", id: f.id, tab: "overview" })}
    actions={(f) => [
      {
        label: "Unassign",
        onclick: () => ask(f, tabs.unassign),
        disabled: f.server === null,
        hint: f.server === null ? "The floating IP is not assigned." : undefined,
      },
      { label: "Delete", onclick: () => ask(f, tabs.delete) },
    ]}
  />
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} floating IP`}
  confirmLabel={pending?.action.label ?? ""}
  onconfirm={run}
>
  {#if pending?.action.id === "delete"}
    <p>The floating IP <strong>{pending.fip.name}</strong> ({pending.fip.ip}) is released for good.</p>
  {:else if pending}
    <p>The floating IP <strong>{pending.fip.name}</strong> is unassigned from its server.</p>
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
