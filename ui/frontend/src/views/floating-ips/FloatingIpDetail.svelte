<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import ReverseDnsDialog from "../../lib/ReverseDnsDialog.svelte";
  import { api, listAll, runApi } from "../../lib/api";
  import { createdText, protocolText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { menu, tabs as fipTabs } from "../../lib/resources/floatingIps";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import FloatingIpActionInput from "./FloatingIpActionInput.svelte";
  import type { FloatingIp } from "./Section.svelte";

  // The detail page of one floating IP: Overview (address with a copy
  // button, protocol, description with an edit dialog, home location,
  // server, reverse DNS, created), Labels and Protection; the actions menu
  // assigns and unassigns it, sets the reverse DNS, changes the protection
  // and deletes it (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Server = { id: number; name: string; location: { name: string; network_zone: string } };

  const path = $derived(`/floating_ips/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "floating-ips", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let fip = $state<FloatingIp | undefined>();
  let servers = $state<Server[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [f, s] = await Promise.all([
        api<{ floating_ip: FloatingIp }>(project, "GET", path),
        listAll<Server>(project, "/servers", "servers"),
      ]);
      fip = f.floating_ip;
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load floating IP", err);
      failing = true;
    }
  }

  // The open floating IP refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      fip = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const serverName = (sid: number) => servers.find((s) => s.id === sid)?.name ?? `${sid}`;

  // ptrIps are the addresses the reverse DNS dialog offers: an IPv4 floating
  // IP's address, and every address of the IPv6 network that has an entry;
  // for an IPv6 network the dialog also offers a new address in it.
  const ptrIps = $derived.by(() => {
    if (!fip) return [];
    const out = fip.type === "ipv4" ? [fip.ip] : [];
    for (const p of fip.dns_ptr) if (!out.includes(p.ip)) out.push(p.ip);
    return out;
  });

  // Unassign is disabled while the IP is not assigned.
  const menuActions = $derived(
    menu.map((a): ResourceAction => {
      if (a.id === "unassign" && fip?.server == null) return { ...a, disabledHint: "The floating IP is not assigned." };
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
    if (!fip || !ptrAction) return;
    const req = request(ptrAction, fip.id, input)!;
    const res = await runApi(project, `${ptrAction.label} ${fip.name}`, req.method, req.path, req.body);
    if (res !== undefined) load();
  }

  // The description dialog of the Overview; it closes before the request ends.
  let descOpen = $state(false);
  let description = $state("");

  function editDescription() {
    description = fip?.description ?? "";
    descOpen = true;
  }

  async function saveDescription(e: SubmitEvent) {
    e.preventDefault();
    descOpen = false;
    if (!fip) return;
    const a = fipTabs.description;
    const req = request(a, fip.id, { description: description.trim() })!;
    const res = await runApi(project, `${a.label} ${fip.name}`, req.method, req.path, req.body);
    if (res !== undefined) load();
  }

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Floating IP" help="floating-ips" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !fip}
  <p class="muted" aria-busy="true">Loading the floating IP…</p>
{:else}
  {@const f = fip}
  <DetailPage title={f.name} help="floating-ips" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={f.id} name={f.name} actions={menuActions} ondone={done} onselect={select}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          <FloatingIpActionInput action={a} fip={f} {servers} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={f.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} flags={[{ key: "delete", label: "Protect against deletion", value: f.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>IP</dt>
        <dd><CopyField value={f.ip} label="address" /></dd>
        <dt>Protocol</dt>
        <dd>{protocolText(f.type)}</dd>
        <dt>Description</dt>
        <dd class="row">
          {#if f.description}<span>{f.description}</span>{:else}<span class="muted">None</span>{/if}
          <Button onclick={editDescription}>Edit description</Button>
        </dd>
        <dt>Home location</dt>
        <dd>{f.home_location.name}</dd>
        <dt>Server</dt>
        <dd>
          {#if f.server === null}
            <span class="muted">Not assigned</span>
          {:else}
            <a href={href({ area: "project", project, view: "servers", page: { kind: "detail", id: f.server, tab: "overview" } })}
              >{serverName(f.server)}</a
            >
          {/if}
        </dd>
        <dt>Reverse DNS</dt>
        <dd>
          {#if f.dns_ptr.length === 0}
            <span class="muted">Default</span>
          {:else}
            {#each f.dns_ptr as p (p.ip)}<div>{p.ip}: {p.dns_ptr}</div>{/each}
          {/if}
        </dd>
        <dt>Created</dt>
        <dd>{createdText(f.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
  <ReverseDnsDialog
    bind:open={ptrOpen}
    ips={ptrIps}
    network={f.type === "ipv6" ? f.ip : undefined}
    current={Object.fromEntries(f.dns_ptr.map((p) => [p.ip, p.dns_ptr]))}
    onsubmit={savePtr}
  />
  <Dialog bind:open={descOpen} title={`Change description ${f.name}`}>
    <form onsubmit={saveDescription}>
      <Field label="Description">
        {#snippet children(c)}
          <input id={c.id} autocomplete="off" bind:value={description} />
        {/snippet}
      </Field>
      <div class="buttons">
        <Button onclick={() => (descOpen = false)}>Cancel</Button>
        <Button type="submit" variant="primary">Save</Button>
      </div>
    </form>
  </Dialog>
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
  .row {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  input {
    width: 100%;
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
