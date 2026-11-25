<script module lang="ts">
  /** PrimaryIp as the Hetzner API returns it; HLab shows its location, no datacenter. */
  export type PrimaryIp = {
    id: number;
    name: string;
    ip: string;
    type: "ipv4" | "ipv6";
    assignee_type: string;
    assignee_id: number | null;
    auto_delete: boolean;
    dns_ptr: { ip: string; dns_ptr: string }[];
    location: { name: string; network_zone: string };
    blocked: boolean;
    protection: { delete: boolean };
    labels: Record<string, string>;
    created: string;
  };

  /** Server is what the primary IP views need of a server. */
  export type Server = {
    id: number;
    name: string;
    status: string;
    location: { name: string };
    public_net: { ipv4: unknown | null; ipv6: unknown | null };
  };

  /** assigneeText names the holder of a primary IP: a server or a load balancer. */
  export function assigneeText(p: PrimaryIp, servers: Server[]): string {
    if (p.assignee_id === null) return "";
    if (p.assignee_type === "load_balancer") return `Load balancer ${p.assignee_id}`;
    return servers.find((s) => s.id === p.assignee_id)?.name ?? `${p.assignee_id}`;
  }

  /** assignable are the servers a primary IP can be assigned to: off, in its location, without a public IP of its family. */
  export function assignable(servers: Server[], location: string, type: string): Server[] {
    return servers.filter(
      (s) => s.status === "off" && s.location.name === location && (type === "ipv6" ? s.public_net.ipv6 : s.public_net.ipv4) == null,
    );
  }
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
  import { rowAction, tabs } from "../../lib/resources/primaryIps";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import PrimaryIpActionInput from "./PrimaryIpActionInput.svelte";
  import PrimaryIpCreate from "./PrimaryIpCreate.svelte";
  import PrimaryIpDetail from "./PrimaryIpDetail.svelte";

  // The "Primary IPs" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "primary-ips", page: { kind: "list" } }));

  let rows = $state<PrimaryIp[] | undefined>();
  let servers = $state<Server[]>([]);
  let failing = false;

  const columns: Column<PrimaryIp>[] = [
    { key: "name", label: "Name", value: (p) => p.name, sortable: true },
    { key: "ip", label: "IP", value: (p) => p.ip, sortable: true },
    { key: "protocol", label: "Protocol", value: (p) => protocolText(p.type), sortable: true },
    { key: "location", label: "Location", value: (p) => p.location.name, sortable: true },
    { key: "assignee", label: "Assignee", value: (p) => assigneeText(p, servers), sortable: true },
    { key: "auto_delete", label: "Auto delete", value: (p) => (p.auto_delete ? "Yes" : "No"), sortable: true },
    { key: "labels", label: "Labels", value: (p) => labelText(p.labels) },
    { key: "created", label: "Created", value: (p) => createdText(p.created), sortable: true },
  ];

  async function load() {
    try {
      const [p, s] = await Promise.all([
        listAll<PrimaryIp>(project, "/primary_ips", "primary_ips"),
        listAll<Server>(project, "/servers", "servers"),
      ]);
      servers = s;
      rows = p;
      failing = false;
    } catch (err) {
      if (!failing) showError("Load primary IPs", err);
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
    location.hash = href({ area: "project", project, view: "primary-ips", page: p });
  }

  // Row menu: Assign (with the server select) while unassigned, Unassign
  // with a plain confirmation while assigned to a server; Delete with the
  // name typed.
  let pending = $state<{ pip: PrimaryIp; action: ResourceAction; input: Input } | undefined>();
  let inputOpen = $state(false);
  let confirmOpen = $state(false);

  function ask(pip: PrimaryIp, action: ResourceAction) {
    pending = { pip, action, input: {} };
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
    const { pip, action, input } = pending;
    const req = request(action, pip.id, input)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, pip.name, action.label, req).then(load);
  }

  // A primary IP held by a load balancer cannot be unassigned.
  const unassignHint = (p: PrimaryIp) =>
    p.assignee_id !== null && p.assignee_type === "load_balancer" ? "Deleting the load balancer releases it." : undefined;
</script>

{#if !$running}
  <div class="title-row">
    <h1>Primary IPs</h1>
    <HelpButton view="primary-ips" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <PrimaryIpCreate {project} {list} />
{:else if page.kind === "detail"}
  <PrimaryIpDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Primary IPs</h1>
    <HelpButton view="primary-ips" />
  </div>
  <Table
    title="Primary IPs"
    {rows}
    {columns}
    key={(p) => p.id}
    name={(p) => p.name}
    labels={(p) => p.labels}
    emptyText="This project has no primary IPs yet."
    createLabel="Create primary IP"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(p) => open({ kind: "detail", id: p.id, tab: "overview" })}
    actions={(p) => [
      {
        label: rowAction(p).label,
        onclick: () => ask(p, rowAction(p)),
        disabled: unassignHint(p) !== undefined,
        hint: unassignHint(p),
      },
      { label: "Delete", onclick: () => ask(p, tabs.delete) },
    ]}
  />
{/if}

<Dialog bind:open={inputOpen} title={`${pending?.action.label ?? ""} ${pending?.pip.name ?? ""}`}>
  {#if pending && inputOpen}
    <PrimaryIpActionInput action={pending.action} pip={pending.pip} {servers} {submit} cancel={() => (inputOpen = false)} />
  {/if}
</Dialog>

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} primary IP`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? pending.pip.name : undefined}
  onconfirm={run}
>
  {#if pending?.action.id === "delete"}
    <p>The primary IP <strong>{pending.pip.name}</strong> ({pending.pip.ip}) is released for good.</p>
  {:else if pending}
    <p>The primary IP <strong>{pending.pip.name}</strong> is unassigned from its server, which must be powered off.</p>
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
