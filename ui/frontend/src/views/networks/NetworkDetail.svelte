<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import RenameInput from "../../lib/RenameInput.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { api, listAll, runApi } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { menu, tabs as tabActions } from "../../lib/resources/networks";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import type { Network } from "./Section.svelte";

  // The detail page of one network: Overview (IP range, subnets and routes
  // counted), Subnets (add with network zone and range, delete), Routes (add
  // destination and gateway, delete), Resources (attached servers and load
  // balancers, read only, linking to their pages), Labels and Protection;
  // the actions menu changes the IP range, the protection and deletes it
  // (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Named = { id: number; name: string };
  type Location = { name: string; network_zone: string };

  const path = $derived(`/networks/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "subnets", label: "Subnets" },
      { id: "routes", label: "Routes" },
      { id: "resources", label: "Resources" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "networks", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let network = $state<Network | undefined>();
  let servers = $state<Named[]>([]);
  let loadBalancers = $state<Named[]>([]);
  let zones = $state<string[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [n, s, l, loc] = await Promise.all([
        api<{ network: Network }>(project, "GET", path),
        listAll<Named>(project, "/servers", "servers"),
        listAll<Named>(project, "/load_balancers", "load_balancers"),
        listAll<Location>(project, "/locations", "locations"),
      ]);
      network = n.network;
      servers = s;
      loadBalancers = l;
      zones = [...new Set(loc.map((x) => x.network_zone))].sort();
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load network", err);
      failing = true;
    }
  }

  // The open network refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      network = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const nameOf = (all: Named[], rid: number) => all.find((r) => r.id === rid)?.name ?? `${rid}`;
  const detail = (view: "servers" | "load-balancers", rid: number) =>
    href({ area: "project", project, view, page: { kind: "detail", id: rid, tab: "overview" } });

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }

  async function run(a: ResourceAction, input: Input) {
    if (!network) return;
    const req = request(a, network.id, input)!;
    const res = await runApi(project, `${a.label} ${network.name}`, req.method, req.path, req.body);
    if (res !== undefined) load();
  }

  // Inputs of the menu's dialogs, set anew each time a dialog opens.
  let ipRange = $state("");
  let protectDelete = $state(false);

  // prefill starts a menu dialog from the network's current values; the
  // menu then opens it as usual.
  function prefill(): boolean {
    ipRange = network?.ip_range ?? "";
    protectDelete = network?.protection.delete ?? false;
    return false;
  }

  // The add subnet and add route dialogs and the delete confirmations.
  let subnetOpen = $state(false);
  let subnetZone = $state("");
  let subnetRange = $state("");
  let routeOpen = $state(false);
  let routeDestination = $state("");
  let routeGateway = $state("");
  let confirmOpen = $state(false);
  let confirming = $state<{ action: ResourceAction; input: Input; text: string } | undefined>();

  function openSubnet() {
    subnetZone = zones[0] ?? "";
    subnetRange = "";
    subnetOpen = true;
  }

  function addSubnet(e: SubmitEvent) {
    e.preventDefault();
    subnetOpen = false;
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(tabActions.add_subnet, { network_zone: subnetZone, ip_range: subnetRange.trim() });
  }

  function openRoute() {
    routeDestination = "";
    routeGateway = "";
    routeOpen = true;
  }

  function addRoute(e: SubmitEvent) {
    e.preventDefault();
    routeOpen = false;
    run(tabActions.add_route, { destination: routeDestination.trim(), gateway: routeGateway.trim() });
  }

  function ask(action: ResourceAction, input: Input, text: string) {
    confirming = { action, input, text };
    confirmOpen = true;
  }
</script>

{#if notFound}
  <DetailPage title="Network" help="networks" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !network}
  <p class="muted" aria-busy="true">Loading the network…</p>
{:else}
  {@const n = network}
  <DetailPage title={n.name} help="networks" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={n.id} name={n.name} actions={menu} ondone={done} onselect={prefill}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          {#if a.id === "rename"}
            <RenameInput current={n.name} {submit} {cancel} />
          {:else}
            <form
              onsubmit={(e) => {
                e.preventDefault();
                if (a.id === "change_ip_range") {
                  if (ipRange.trim() !== "") submit({ ip_range: ipRange.trim() });
                } else submit({ delete: protectDelete });
              }}
            >
              {#if a.id === "change_ip_range"}
                <Field label="IP range" hint="Must contain the current range; refused while servers are attached.">
                  {#snippet children(c)}
                    <input id={c.id} aria-describedby={c.describedby} bind:value={ipRange} />
                  {/snippet}
                </Field>
              {:else}
                <label class="check"><input type="checkbox" bind:checked={protectDelete} />Protect against deletion</label>
              {/if}
              <div class="buttons">
                <Button onclick={cancel}>Cancel</Button>
                <Button type="submit" variant="primary">{a.label}</Button>
              </div>
            </form>
          {/if}
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "subnets"}
      <section>
        <div class="head">
          <h2>Subnets</h2>
          <Button onclick={openSubnet}>Add subnet</Button>
        </div>
        {#if n.subnets.length === 0}
          <p class="muted">The network has no subnets.</p>
        {:else}
          <table>
            <thead>
              <tr><th>IP range</th><th>Network zone</th><th>Type</th><th>Gateway</th><th><span class="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {#each n.subnets as s (s.ip_range)}
                <tr>
                  <td>{s.ip_range}</td>
                  <td>{s.network_zone}</td>
                  <td>{s.type}</td>
                  <td>{s.gateway}</td>
                  <td>
                    <Button variant="danger" onclick={() => ask(tabActions.delete_subnet, { ip_range: s.ip_range }, `the subnet ${s.ip_range}`)}
                      >Delete</Button
                    >
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    {:else if current === "routes"}
      <section>
        <div class="head">
          <h2>Routes</h2>
          <Button onclick={openRoute}>Add route</Button>
        </div>
        {#if n.routes.length === 0}
          <p class="muted">The network has no routes.</p>
        {:else}
          <table>
            <thead>
              <tr><th>Destination</th><th>Gateway</th><th><span class="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {#each n.routes as r (`${r.destination} ${r.gateway}`)}
                <tr>
                  <td>{r.destination}</td>
                  <td>{r.gateway}</td>
                  <td>
                    <Button
                      variant="danger"
                      onclick={() => ask(tabActions.delete_route, { destination: r.destination, gateway: r.gateway }, `the route to ${r.destination}`)}
                      >Delete</Button
                    >
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    {:else if current === "resources"}
      <section>
        <h2>Servers</h2>
        {#if n.servers.length === 0}
          <p class="muted">No server is attached.</p>
        {:else}
          <ul>
            {#each n.servers as sid (sid)}<li><a href={detail("servers", sid)}>{nameOf(servers, sid)}</a></li>{/each}
          </ul>
        {/if}
        <h2>Load balancers</h2>
        {#if n.load_balancers.length === 0}
          <p class="muted">No load balancer is attached.</p>
        {:else}
          <ul>
            {#each n.load_balancers as lid (lid)}<li><a href={detail("load-balancers", lid)}>{nameOf(loadBalancers, lid)}</a></li>{/each}
          </ul>
        {/if}
      </section>
    {:else if current === "labels"}
      <LabelsTab {project} {path} labels={n.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} flags={[{ key: "delete", label: "Protect against deletion", value: n.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>IP range</dt>
        <dd>{n.ip_range}</dd>
        <dt>Subnets</dt>
        <dd>{n.subnets.length}</dd>
        <dt>Routes</dt>
        <dd>{n.routes.length}</dd>
        <dt>Attached</dt>
        <dd>{n.servers.length} servers, {n.load_balancers.length} load balancers</dd>
        <dt>Created</dt>
        <dd>{createdText(n.created)}</dd>
      </dl>
    {/if}
  </DetailPage>

  <Dialog bind:open={subnetOpen} title={`Add a subnet to ${n.name}`}>
    <form onsubmit={addSubnet}>
      <Field label="Network zone">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={subnetZone}>
            {#each zones as z (z)}<option value={z}>{z}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field label="IP range" hint={`A range inside ${n.ip_range}, for example a /24.`}>
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} bind:value={subnetRange} />
        {/snippet}
      </Field>
      <div class="buttons">
        <Button onclick={() => (subnetOpen = false)}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={subnetZone === "" || subnetRange.trim() === ""}>Add subnet</Button>
      </div>
    </form>
  </Dialog>

  <Dialog bind:open={routeOpen} title={`Add a route to ${n.name}`}>
    <form onsubmit={addRoute}>
      <Field label="Destination" hint="A range in CIDR notation.">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} bind:value={routeDestination} />
        {/snippet}
      </Field>
      <Field label="Gateway" hint={`An address inside ${n.ip_range}.`}>
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} bind:value={routeGateway} />
        {/snippet}
      </Field>
      <div class="buttons">
        <Button onclick={() => (routeOpen = false)}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={routeDestination.trim() === "" || routeGateway.trim() === ""}>Add route</Button>
      </div>
    </form>
  </Dialog>

  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirming ? `${confirming.action.label} of ${n.name}` : ""}
    confirmLabel="Delete"
    onconfirm={() => {
      if (confirming) run(confirming.action, confirming.input);
    }}
  >
    <p>Delete <strong>{confirming?.text ?? ""}</strong> from <strong>{n.name}</strong>?</p>
  </ConfirmDialog>
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
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 800px;
    margin-bottom: var(--s3);
  }
  table {
    border-collapse: collapse;
    max-width: 800px;
    width: 100%;
  }
  th,
  td {
    text-align: left;
    padding: var(--s2);
    border-bottom: 1px solid var(--border);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
  .muted {
    color: var(--muted);
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
</style>
