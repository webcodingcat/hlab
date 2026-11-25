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
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { createdText, labelText, protocolText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { rowAction, tabs } from "../../lib/resources/floatingIps";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import FloatingIpActionInput from "./FloatingIpActionInput.svelte";
  import FloatingIpCreate from "./FloatingIpCreate.svelte";
  import FloatingIpDetail from "./FloatingIpDetail.svelte";

  // The "Floating IPs" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "floating-ips", page: { kind: "list" } }));

  let rows = $state<FloatingIp[] | undefined>();
  type Server = { id: number; name: string; location: { name: string; network_zone: string } };
  let servers = $state<Server[]>([]);
  let failing = false;

  const serverText = (f: FloatingIp) => (f.server === null ? "" : (servers.find((s) => s.id === f.server)?.name ?? `${f.server}`));

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
        listAll<Server>(project, "/servers", "servers"),
      ]);
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
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

  // Row menu: Assign (with the server select) while unassigned, Unassign
  // with a plain confirmation while assigned; Delete with the name typed.
  let pending = $state<{ fip: FloatingIp; action: ResourceAction; input: Input } | undefined>();
  let inputOpen = $state(false);
  let confirmOpen = $state(false);

  function ask(fip: FloatingIp, action: ResourceAction) {
    pending = { fip, action, input: {} };
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
    const { fip, action, input } = pending;
    const req = request(action, fip.id, input);
    if (!req) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, fip.name, action.label, req).then(load);
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
      { label: rowAction(f).label, onclick: () => ask(f, rowAction(f)) },
      { label: "Delete", onclick: () => ask(f, tabs.delete) },
    ]}
  />
{/if}

<Dialog bind:open={inputOpen} title={`${pending?.action.label ?? ""} ${pending?.fip.name ?? ""}`}>
  {#if pending && inputOpen}
    <FloatingIpActionInput action={pending.action} fip={pending.fip} {servers} {submit} cancel={() => (inputOpen = false)} />
  {/if}
</Dialog>

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} floating IP`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? pending.fip.name : undefined}
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
