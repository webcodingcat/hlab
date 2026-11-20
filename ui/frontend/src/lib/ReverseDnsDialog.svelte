<script lang="ts">
  import { untrack } from "svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";
  import type { Input } from "./resourceActions";

  // The reverse DNS dialog of a resource with public IPs: an IP select and
  // the host name for it, prefilled with its current entry. An empty host
  // name resets the entry to the default. With network (an IPv6 network
  // such as 2001:db8::/64) the select also offers a new address, typed into
  // an Address field, so the network's first entry can be set. onsubmit gets
  // { ip, dns_ptr } after the dialog has closed.
  type Props = {
    open: boolean;
    ips: string[];
    current: Record<string, string | null>;
    network?: string;
    onsubmit: (input: Input) => void;
  };

  let { open = $bindable(), ips, current, network, onsubmit }: Props = $props();

  // ip is the chosen address; NEW stands for a new address in network.
  const NEW = "new";
  let ip = $state("");
  let address = $state("");
  let ptr = $state("");

  const target = $derived(ip === NEW ? address.trim() : ip);

  // Each time the dialog opens it starts at the first IP and its entry.
  $effect(() => {
    if (!open) return;
    untrack(() => {
      ip = ips[0] ?? (network ? NEW : "");
      address = "";
      ptr = current[ip] ?? "";
    });
  });

  function chooseIP() {
    ptr = current[ip] ?? "";
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    open = false;
    onsubmit({ ip: target, dns_ptr: ptr.trim() });
  }
</script>

<Dialog bind:open title="Reverse DNS">
  <form onsubmit={submit}>
    <Field label="IP">
      {#snippet children(c)}
        <select id={c.id} bind:value={ip} onchange={chooseIP}>
          {#each ips as i (i)}<option value={i}>{i}</option>{/each}
          {#if network}<option value={NEW}>New address in {network}</option>{/if}
        </select>
      {/snippet}
    </Field>
    {#if ip === NEW}
      <Field label="Address" hint={`An address in ${network}.`}>
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={address} />
        {/snippet}
      </Field>
    {/if}
    <Field label="Host name" hint="For example host.example.com; empty resets it to the default.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ptr} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (open = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={target === ""}>Save</Button>
    </div>
  </form>
</Dialog>

<style>
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
