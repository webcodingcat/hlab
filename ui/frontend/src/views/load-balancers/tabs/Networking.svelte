<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { runOn } from "../../../lib/api";
  import { request, type Input, type ResourceAction } from "../../../lib/resourceActions";
  import { tabs } from "../../../lib/resources/loadBalancers";
  import { href } from "../../../state";
  import type { LoadBalancer } from "../Section.svelte";

  // The Networking tab: the private networks of the load balancer with its
  // IP in each; attach to a network of its network zone (IP optional) and
  // detach after a confirmation.
  type Network = { id: number; name: string; ip_range: string; subnets: { network_zone: string }[] };
  type Props = { project: string; lb: LoadBalancer; networks: Network[]; onchanged: () => void };

  let { project, lb, networks, onchanged }: Props = $props();

  let attachOpen = $state(false);
  let network = $state("");
  let ip = $state("");
  let detachOpen = $state(false);
  let detachNet = $state<number | undefined>();

  const zone = $derived(lb.location.network_zone);
  const free = $derived(
    networks.filter((n) => !lb.private_net.some((p) => p.network === n.id) && n.subnets.some((s) => s.network_zone === zone)),
  );
  const netName = (id: number | undefined) => networks.find((n) => n.id === id)?.name ?? `network ${id}`;

  async function run(a: ResourceAction, input: Input) {
    const req = request(a, lb.id, input)!;
    const res = await runOn(project, lb.name, a.label, req);
    if (res !== undefined) onchanged();
  }

  function openAttach() {
    network = "";
    ip = "";
    attachOpen = true;
  }

  function attach(e: SubmitEvent) {
    e.preventDefault();
    if (network === "") return;
    attachOpen = false;
    const input: Input = { network: Number(network) };
    if (ip.trim() !== "") input.ip = ip.trim();
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(tabs.attach_to_network, input);
  }

  function askDetach(id: number) {
    detachNet = id;
    detachOpen = true;
  }
</script>

<section>
  <div class="head">
    <h2>Private networks</h2>
    <Button onclick={openAttach}>Attach to network</Button>
  </div>
  {#if lb.private_net.length === 0}
    <p class="muted">The load balancer is in no private network.</p>
  {:else}
    <table>
      <thead><tr><th>Network</th><th>IP</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each lb.private_net as p (p.network)}
          <tr>
            <td><a href={href({ area: "project", project, view: "networks", page: { kind: "detail", id: p.network, tab: "overview" } })}>{netName(p.network)}</a></td>
            <td>{p.ip}</td>
            <td><Button variant="danger" onclick={() => askDetach(p.network)}>Detach</Button></td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={attachOpen} title={`Attach ${lb.name} to a network`}>
  <form onsubmit={attach}>
    <Field label="Network" hint={`Networks with a subnet in ${zone}.`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={network}>
          <option value="" disabled>Choose a network</option>
          {#each free as n (n.id)}<option value={`${n.id}`}>{n.name} ({n.ip_range})</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field label="IP" hint="Optional; empty picks a free address of the subnet.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ip} />
      {/snippet}
    </Field>
    <div class="buttons">
      <Button onclick={() => (attachOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={network === ""}>Attach</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={detachOpen}
  title={`Detach ${lb.name}`}
  confirmLabel="Detach"
  onconfirm={() => {
    if (detachNet !== undefined) run(tabs.detach_from_network, { network: detachNet });
  }}
>
  <p>Detach <strong>{lb.name}</strong> from <strong>{netName(detachNet)}</strong>?</p>
</ConfirmDialog>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 640px;
    margin-bottom: var(--s3);
  }
  table {
    border-collapse: collapse;
    max-width: 640px;
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
  input {
    width: 100%;
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
