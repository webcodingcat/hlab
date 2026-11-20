<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import CopyField from "../../../lib/CopyField.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Named, Server } from "../servers";
  import { runTab, splitIPs } from "./run";

  // The Networking tab: the public IPs with their reverse DNS (edit, reset
  // to default) and the private networks (attach, change alias IPs, detach).
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let networks = $state<Named[]>([]);

  // Reverse DNS dialog: ip is fixed for an existing entry, typed for a new IPv6 one.
  let ptrOpen = $state(false);
  let ptrNew = $state(false);
  let ptrIP = $state("");
  let ptrName = $state("");

  // Network dialog: attach (network, ip, alias IPs) or change alias IPs of one network.
  let netOpen = $state(false);
  let netMode = $state<"attach" | "alias">("attach");
  let network = $state("");
  let ip = $state("");
  let aliases = $state("");

  let detachOpen = $state(false);
  let detachNet = $state<number | undefined>();

  const ptrRows = $derived([
    ...(server.public_net.ipv4 ? [{ ip: server.public_net.ipv4.ip, dns_ptr: server.public_net.ipv4.dns_ptr, v4: true }] : []),
    ...(server.public_net.ipv6?.dns_ptr ?? []).map((e) => ({ ...e, v4: false })),
  ]);
  const free = $derived(networks.filter((n) => !server.private_net.some((p) => p.network === n.id)));
  const netName = (id: number | undefined) => networks.find((n) => n.id === id)?.name ?? `network ${id}`;

  async function loadNetworks() {
    try {
      networks = await listAll<Named>(project, "/networks", "networks");
    } catch (err) {
      showError("Load networks", err);
    }
  }

  $effect(() => {
    loadNetworks();
  });

  async function run(id: string, input: Record<string, unknown>) {
    if (await runTab(project, server, id, input)) onchanged();
  }

  function editPtr(row?: { ip: string; dns_ptr: string }) {
    ptrNew = !row;
    ptrIP = row?.ip ?? "";
    ptrName = row?.dns_ptr ?? "";
    ptrOpen = true;
  }

  function savePtr(e: SubmitEvent) {
    e.preventDefault();
    ptrOpen = false;
    run("change_dns_ptr", { ip: ptrIP.trim(), dns_ptr: ptrName.trim() });
  }

  function openNet(mode: "attach" | "alias", n?: { network: number; alias_ips: string[] }) {
    netMode = mode;
    network = n ? `${n.network}` : "";
    ip = "";
    aliases = n ? n.alias_ips.join(", ") : "";
    netOpen = true;
    loadNetworks();
  }

  function saveNet(e: SubmitEvent) {
    e.preventDefault();
    netOpen = false;
    const alias_ips = splitIPs(aliases);
    if (netMode === "alias") {
      run("change_alias_ips", { network: Number(network), alias_ips });
      return;
    }
    run("attach_to_network", {
      network: Number(network),
      ip: ip.trim() || undefined,
      alias_ips: alias_ips.length > 0 ? alias_ips : undefined,
    });
  }
</script>

<section>
  <header>
    <h2>Public IPs</h2>
    {#if server.public_net.ipv6}<Button onclick={() => editPtr()}>Add IPv6 reverse DNS</Button>{/if}
  </header>
  {#if ptrRows.length === 0}
    <p class="muted">This server has no public IP.</p>
  {:else}
    {#if server.public_net.ipv6}<p class="muted">IPv6 network <span class="mono">{server.public_net.ipv6.ip}</span></p>{/if}
    <table>
      <thead><tr><th>IP</th><th>Reverse DNS</th><th><span class="hidden">Actions</span></th></tr></thead>
      <tbody>
        {#each ptrRows as r (r.ip)}
          <tr>
            <td><CopyField value={r.ip} label="IP" /></td>
            <td class="mono">{r.dns_ptr}</td>
            <td class="row-actions">
              <Button onclick={() => editPtr(r)}>Edit</Button>
              <Button onclick={() => run("change_dns_ptr", { ip: r.ip, dns_ptr: null })}>
                {r.v4 ? "Reset to default" : "Remove"}
              </Button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<section>
  <header>
    <h2>Private networks</h2>
    <Button variant="primary" onclick={() => openNet("attach")}>Attach to network</Button>
  </header>
  {#if server.private_net.length === 0}
    <p class="muted">This server is in no private network.</p>
  {:else}
    <table>
      <thead><tr><th>Network</th><th>IP</th><th>Alias IPs</th><th><span class="hidden">Actions</span></th></tr></thead>
      <tbody>
        {#each server.private_net as n (n.network)}
          <tr>
            <td>{netName(n.network)}</td>
            <td class="mono">{n.ip}</td>
            <td class="mono">{n.alias_ips.join(", ") || "-"}</td>
            <td class="row-actions">
              <Button onclick={() => openNet("alias", n)}>Change alias IPs</Button>
              <Button
                onclick={() => {
                  detachNet = n.network;
                  detachOpen = true;
                }}>Detach</Button
              >
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={ptrOpen} title={ptrNew ? "Add IPv6 reverse DNS" : `Edit reverse DNS of ${ptrIP}`}>
  <form onsubmit={savePtr}>
    {#if ptrNew}
      <Field label="IP" hint={`An address in ${server.public_net.ipv6?.ip ?? ""}`}>
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ptrIP} />
        {/snippet}
      </Field>
    {/if}
    <Field label="Host name" hint="For example server.example.com">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ptrName} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (ptrOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={!ptrIP.trim() || !ptrName.trim()}>Save</Button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={netOpen} title={netMode === "attach" ? "Attach to network" : `Change alias IPs in ${netName(Number(network))}`}>
  <form onsubmit={saveNet}>
    {#if netMode === "attach"}
      <Field label="Network">
        {#snippet children(c)}
          <select id={c.id} bind:value={network}>
            <option value="" disabled>Choose a network</option>
            {#each free as n (n.id)}<option value={`${n.id}`}>{n.name}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field label="IP" hint="Optional; empty takes the next free address.">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ip} />
        {/snippet}
      </Field>
    {/if}
    <Field label="Alias IPs" hint="Optional; separated by commas.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={aliases} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (netOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={network === ""}>
        {netMode === "attach" ? "Attach" : "Save"}
      </Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={detachOpen}
  title="Detach from network"
  confirmLabel="Detach"
  onconfirm={() => {
    run("detach_from_network", { network: detachNet });
  }}
>
  <p><strong>{server.name}</strong> is detached from {netName(detachNet)} and loses its private IPs there.</p>
</ConfirmDialog>

<style>
  section {
    max-width: 960px;
    margin-bottom: var(--s6);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--s3);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  th,
  td {
    text-align: left;
    padding: var(--s2) var(--s3);
    border-bottom: 1px solid var(--border);
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 12px;
  }
  .row-actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
  .mono {
    font-family: var(--mono);
  }
  .muted {
    color: var(--muted);
  }
  .hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  select,
  input {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
