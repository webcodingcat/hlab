<script module lang="ts">
  /** Target of a load balancer; a label selector lists the servers it matched under targets. */
  export type LoadBalancerTarget = {
    type: "server" | "label_selector" | "ip";
    server?: { id: number; ip: string };
    label_selector?: { selector: string };
    ip?: { ip: string };
    use_private_ip?: boolean;
    health_status?: { listen_port: number; status: string }[];
    targets?: LoadBalancerTarget[];
  };

  /** Service of a load balancer as the API returns it. */
  export type LoadBalancerService = {
    protocol: "tcp" | "http" | "https";
    listen_port: number;
    destination_port: number;
    proxyprotocol: boolean;
    health_check: {
      protocol: "tcp" | "http";
      port: number;
      interval: number;
      timeout: number;
      retries: number;
      http?: { domain: string | null; path: string; tls: boolean } | null;
    };
    http?: {
      sticky_sessions: boolean;
      cookie_name: string;
      cookie_lifetime: number;
      certificates: number[];
      redirect_http: boolean;
    } | null;
  };

  /** Load balancer as the Hetzner API returns it (the fields the views use). */
  export type LoadBalancer = {
    id: number;
    name: string;
    public_net: {
      enabled: boolean;
      ipv4: { ip: string | null; dns_ptr: string | null };
      ipv6: { ip: string | null; dns_ptr: string | null };
    };
    private_net: { network: number; ip: string }[];
    location: { name: string; network_zone: string };
    load_balancer_type: { name: string; description: string };
    algorithm: { type: string };
    targets: LoadBalancerTarget[];
    services: LoadBalancerService[];
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
  import { tabs } from "../../lib/resources/loadBalancers";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import LoadBalancerCreate from "./LoadBalancerCreate.svelte";
  import LoadBalancerDetail from "./LoadBalancerDetail.svelte";

  // The "Load Balancers" section of a project console; page says which of
  // its pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "load-balancers", page: { kind: "list" } }));

  let rows = $state<LoadBalancer[] | undefined>();
  let failing = false;

  const servicesText = (lb: LoadBalancer) => lb.services.map((s) => `${s.protocol} ${s.listen_port}`).join(", ");

  const columns: Column<LoadBalancer>[] = [
    { key: "name", label: "Name", value: (lb) => lb.name, sortable: true },
    { key: "ipv4", label: "Public IPv4", value: (lb) => (lb.public_net.enabled ? (lb.public_net.ipv4.ip ?? "") : ""), sortable: true },
    { key: "type", label: "Type", value: (lb) => lb.load_balancer_type.name, sortable: true },
    { key: "location", label: "Location", value: (lb) => lb.location.name, sortable: true },
    { key: "targets", label: "Targets", value: (lb) => lb.targets.length, sortable: true },
    { key: "services", label: "Services", value: servicesText },
    { key: "labels", label: "Labels", value: (lb) => labelText(lb.labels) },
    { key: "created", label: "Created", value: (lb) => createdText(lb.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<LoadBalancer>(project, "/load_balancers", "load_balancers");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load load balancers", err);
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
    location.hash = href({ area: "project", project, view: "load-balancers", page: p });
  }

  // Row menu: Delete with a plain confirmation.
  let pending = $state<LoadBalancer | undefined>();
  let confirmOpen = $state(false);

  function ask(lb: LoadBalancer) {
    pending = lb;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const lb = pending;
    const req = request(tabs.delete, lb.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, lb.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Load Balancers</h1>
    <HelpButton view="load-balancers" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <LoadBalancerCreate {project} {list} />
{:else if page.kind === "detail"}
  <LoadBalancerDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Load Balancers</h1>
    <HelpButton view="load-balancers" />
  </div>
  <Table
    title="Load Balancers"
    {rows}
    {columns}
    key={(lb) => lb.id}
    name={(lb) => lb.name}
    labels={(lb) => lb.labels}
    emptyText="This project has no load balancers yet."
    createLabel="Create load balancer"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(lb) => open({ kind: "detail", id: lb.id, tab: "overview" })}
    actions={(lb) => [{ label: "Delete", onclick: () => ask(lb) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete load balancer" confirmLabel="Delete" onconfirm={run}>
  {#if pending}
    <p>The load balancer <strong>{pending.name}</strong> and its public IPs are deleted for good.</p>
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
