<script lang="ts">
  import { untrack } from "svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import RenameInput from "../../lib/RenameInput.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import ReverseDnsDialog from "../../lib/ReverseDnsDialog.svelte";
  import { api, listAll, runOn } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/loadBalancers";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import LoadBalancerActionInput from "./LoadBalancerActionInput.svelte";
  import type { LoadBalancer, LoadBalancerTarget } from "./Section.svelte";
  import Graphs from "./tabs/Graphs.svelte";
  import Networking from "./tabs/Networking.svelte";
  import Services from "./tabs/Services.svelte";
  import Targets from "./tabs/Targets.svelte";

  // The detail page of one load balancer: Overview (public IPs with their
  // reverse DNS, type, algorithm, location, public interface, target
  // health), Targets, Services, Networking, Graphs, Labels and Protection;
  // the actions menu changes the type and the algorithm, enables and
  // disables the public interface, sets the reverse DNS, changes the
  // protection and deletes it (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type LoadBalancerType = { id: number; name: string; description: string };
  type Network = { id: number; name: string; ip_range: string; subnets: { network_zone: string }[] };

  const path = $derived(`/load_balancers/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "targets", label: "Targets" },
      { id: "services", label: "Services" },
      { id: "networking", label: "Networking" },
      { id: "graphs", label: "Graphs" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "load-balancers", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let lb = $state<LoadBalancer | undefined>();
  let types = $state<LoadBalancerType[]>([]);
  let networks = $state<Network[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [l, t, n] = await Promise.all([
        api<{ load_balancer: LoadBalancer }>(project, "GET", path),
        listAll<LoadBalancerType>(project, "/load_balancer_types", "load_balancer_types"),
        listAll<Network>(project, "/networks", "networks"),
      ]);
      lb = l.load_balancer;
      types = t.sort((a, b) => a.name.localeCompare(b.name));
      networks = n.sort((a, b) => a.name.localeCompare(b.name));
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load load balancer", err);
      failing = true;
    }
  }

  // The open load balancer refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      lb = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // ips are the public IPs with their reverse DNS entries.
  const ips = $derived.by(() => {
    if (!lb) return [];
    const out: { label: string; ip: string; dns_ptr: string | null }[] = [];
    if (lb.public_net.ipv4.ip) out.push({ label: "IPv4", ip: lb.public_net.ipv4.ip, dns_ptr: lb.public_net.ipv4.dns_ptr });
    if (lb.public_net.ipv6.ip) out.push({ label: "IPv6", ip: lb.public_net.ipv6.ip, dns_ptr: lb.public_net.ipv6.dns_ptr });
    return out;
  });

  // health counts the targets (a label selector by the servers it matched)
  // as healthy when every service reports healthy, unhealthy when one
  // reports unhealthy and unknown otherwise.
  const health = $derived.by(() => {
    const counts = { healthy: 0, unhealthy: 0, unknown: 0 };
    const visit = (t: LoadBalancerTarget) => {
      if (t.type === "label_selector") {
        (t.targets ?? []).forEach(visit);
        return;
      }
      const st = (t.health_status ?? []).map((h) => h.status);
      if (st.length > 0 && st.every((s) => s === "healthy")) counts.healthy++;
      else if (st.includes("unhealthy")) counts.unhealthy++;
      else counts.unknown++;
    };
    (lb?.targets ?? []).forEach(visit);
    return counts;
  });
  const healthText = $derived(
    health.healthy + health.unhealthy + health.unknown === 0
      ? "No targets"
      : `${health.healthy} healthy, ${health.unhealthy} unhealthy, ${health.unknown} unknown`,
  );
  const tone = $derived(health.unhealthy > 0 ? "warning" : "success");

  // Enable is disabled while the public interface is on, Disable while it
  // is off; Reverse DNS needs a public IP.
  const menuActions = $derived(
    menu.map((a): ResourceAction => {
      if (a.id === "enable_public_interface" && lb?.public_net.enabled) return { ...a, disabledHint: "The public interface is enabled." };
      if (a.id === "disable_public_interface" && !lb?.public_net.enabled)
        return { ...a, disabledHint: "The public interface is disabled." };
      if (a.id === "change_dns_ptr" && ips.length === 0) return { ...a, disabledHint: "The load balancer has no public IP." };
      return a;
    }),
  );

  // Reverse DNS opens its own dialog; the request runs after it closed.
  let ptrOpen = $state(false);
  let ptrAction: ResourceAction | undefined;

  function select(a: ResourceAction): boolean {
    if (a.id !== "change_dns_ptr") return false;
    ptrAction = a;
    ptrOpen = true;
    return true;
  }

  async function savePtr(input: Input) {
    if (!lb || !ptrAction) return;
    const req = request(ptrAction, lb.id, input)!;
    const res = await runOn(project, lb.name, ptrAction.label, req);
    if (res !== undefined) load();
  }

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }

  const algorithmText = (t: string) => (t === "least_connections" ? "Least connections" : "Round robin");
</script>

{#if notFound}
  <DetailPage title="Load balancer" help="load-balancers" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !lb}
  <p class="muted" aria-busy="true">Loading the load balancer…</p>
{:else}
  {@const l = lb}
  <DetailPage title={l.name} status={{ tone, text: healthText }} help="load-balancers" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={l.id} name={l.name} actions={menuActions} ondone={done} onselect={select}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          {#if a.id === "rename"}
            <RenameInput current={l.name} {submit} {cancel} />
          {:else}
            <LoadBalancerActionInput action={a} lb={l} {types} {submit} {cancel} />
          {/if}
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "targets"}
      <Targets {project} lb={l} onchanged={load} />
    {:else if current === "services"}
      <Services {project} lb={l} onchanged={load} />
    {:else if current === "networking"}
      <Networking {project} lb={l} {networks} onchanged={load} />
    {:else if current === "graphs"}
      <Graphs {project} id={l.id} />
    {:else if current === "labels"}
      <LabelsTab {project} {path} labels={l.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} name={l.name} flags={[{ key: "delete", label: "Protect against deletion", value: l.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        {#each ips as i (i.ip)}
          <dt>{i.label}</dt>
          <dd>
            <CopyField value={i.ip} label={`${i.label} address`} />
            <div class="muted">Reverse DNS: {i.dns_ptr ?? "default"}</div>
          </dd>
        {/each}
        <dt>Public interface</dt>
        <dd>{l.public_net.enabled ? "Enabled" : "Disabled"}</dd>
        <dt>Type</dt>
        <dd>{l.load_balancer_type.name} ({l.load_balancer_type.description})</dd>
        <dt>Algorithm</dt>
        <dd>{algorithmText(l.algorithm.type)}</dd>
        <dt>Location</dt>
        <dd>{l.location.name}</dd>
        <dt>Target health</dt>
        <dd>{healthText}</dd>
        <dt>Created</dt>
        <dd>{createdText(l.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
  <ReverseDnsDialog
    bind:open={ptrOpen}
    ips={ips.map((i) => i.ip)}
    current={Object.fromEntries(ips.map((i) => [i.ip, i.dns_ptr]))}
    onsubmit={savePtr}
  />
{/if}

<style>
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--s2) var(--s4);
    max-width: 640px;
    margin: 0;
  }
  dt {
    color: var(--muted);
    font-weight: 600;
  }
  dd {
    margin: 0;
    min-width: 0;
  }
  .muted {
    color: var(--muted);
  }
</style>
