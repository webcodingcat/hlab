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
  import { createdText, protocolText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { menu, tabs as pipTabs } from "../../lib/resources/primaryIps";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import PrimaryIpActionInput from "./PrimaryIpActionInput.svelte";
  import { assigneeText, type PrimaryIp, type Server } from "./Section.svelte";

  // The detail page of one primary IP: Overview (address with a copy
  // button, protocol, location, assignee, auto delete switch, reverse DNS,
  // created), Labels and Protection; the actions menu assigns and unassigns
  // it, sets the reverse DNS, changes the protection and deletes it (back to
  // the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  const path = $derived(`/primary_ips/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "primary-ips", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let pip = $state<PrimaryIp | undefined>();
  let servers = $state<Server[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [p, s] = await Promise.all([
        api<{ primary_ip: PrimaryIp }>(project, "GET", path),
        listAll<Server>(project, "/servers", "servers"),
      ]);
      pip = p.primary_ip;
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load primary IP", err);
      failing = true;
    }
  }

  // The open primary IP refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      pip = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // ptrIps are the addresses the reverse DNS dialog offers: an IPv4's
  // address, and the addresses of an IPv6 network that have an entry; a new
  // address of the IPv6 network is entered in the dialog.
  const ptrIps = $derived.by(() => {
    if (!pip) return [];
    const out = pip.type === "ipv4" ? [pip.ip] : [];
    for (const p of pip.dns_ptr) if (!out.includes(p.ip)) out.push(p.ip);
    return out;
  });

  // Assign needs an unassigned IP; Unassign an IP held by a server.
  const menuActions = $derived(
    menu.map((a): ResourceAction => {
      if (a.id === "assign" && pip?.assignee_id != null) return { ...a, disabledHint: "The primary IP is assigned; unassign it first." };
      if (a.id === "unassign" && pip?.assignee_id == null) return { ...a, disabledHint: "The primary IP is not assigned." };
      if (a.id === "unassign" && pip?.assignee_type === "load_balancer")
        return { ...a, disabledHint: "Deleting the load balancer releases it." };
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
    if (!pip || !ptrAction) return;
    const req = request(ptrAction, pip.id, input)!;
    const res = await runOn(project, pip.name, ptrAction.label, req);
    if (res !== undefined) load();
  }

  // The auto delete switch of the Overview sends the new value at once.
  async function setAutoDelete(e: Event) {
    if (!pip) return;
    const on = (e.currentTarget as HTMLInputElement).checked;
    const a = pipTabs.auto_delete;
    const req = request(a, pip.id, { auto_delete: on })!;
    await runOn(project, pip.name, a.label, req);
    load();
  }

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Primary IP" help="primary-ips" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !pip}
  <p class="muted" aria-busy="true">Loading the primary IP…</p>
{:else}
  {@const p = pip}
  <DetailPage title={p.name} help="primary-ips" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={p.id} name={p.name} actions={menuActions} ondone={done} onselect={select}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          {#if a.id === "rename"}
            <RenameInput current={p.name} {submit} {cancel} />
          {:else}
            <PrimaryIpActionInput action={a} pip={p} {servers} {submit} {cancel} />
          {/if}
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={p.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} name={p.name} flags={[{ key: "delete", label: "Protect against deletion", value: p.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>IP</dt>
        <dd><CopyField value={p.ip} label="address" /></dd>
        <dt>Protocol</dt>
        <dd>{protocolText(p.type)}</dd>
        <dt>Location</dt>
        <dd>{p.location.name}</dd>
        <dt>Assignee</dt>
        <dd>
          {#if p.assignee_id === null}
            <span class="muted">Not assigned</span>
          {:else if p.assignee_type === "load_balancer"}
            <a href={href({ area: "project", project, view: "load-balancers", page: { kind: "detail", id: p.assignee_id, tab: "overview" } })}
              >{assigneeText(p, servers)}</a
            >
          {:else}
            <a href={href({ area: "project", project, view: "servers", page: { kind: "detail", id: p.assignee_id, tab: "overview" } })}
              >{assigneeText(p, servers)}</a
            >
          {/if}
        </dd>
        <dt>Auto delete</dt>
        <dd>
          <label class="check">
            <input type="checkbox" role="switch" checked={p.auto_delete} onchange={setAutoDelete} />Delete the IP together with its
            server
          </label>
        </dd>
        <dt>Reverse DNS</dt>
        <dd>
          {#if p.dns_ptr.length === 0}
            <span class="muted">Default</span>
          {:else}
            {#each p.dns_ptr as d (d.ip)}<div>{d.ip}: {d.dns_ptr}</div>{/each}
          {/if}
        </dd>
        <dt>Created</dt>
        <dd>{createdText(p.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
  <ReverseDnsDialog
    bind:open={ptrOpen}
    ips={ptrIps}
    current={Object.fromEntries(p.dns_ptr.map((d) => [d.ip, d.dns_ptr]))}
    network={p.type === "ipv6" ? p.ip : undefined}
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
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
</style>
