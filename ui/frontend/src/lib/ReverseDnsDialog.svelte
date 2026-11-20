<script lang="ts">
  import { untrack } from "svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";
  import type { Input } from "./resourceActions";

  // The reverse DNS dialog of a resource with public IPs: an IP select and
  // the host name for it, prefilled with its current entry. An empty host
  // name resets the entry to the default. onsubmit gets { ip, dns_ptr }
  // after the dialog has closed.
  type Props = {
    open: boolean;
    ips: string[];
    current: Record<string, string | null>;
    onsubmit: (input: Input) => void;
  };

  let { open = $bindable(), ips, current, onsubmit }: Props = $props();

  let ip = $state("");
  let ptr = $state("");

  // Each time the dialog opens it starts at the first IP and its entry.
  $effect(() => {
    if (!open) return;
    untrack(() => {
      ip = ips[0] ?? "";
      ptr = current[ip] ?? "";
    });
  });

  function chooseIP() {
    ptr = current[ip] ?? "";
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    open = false;
    onsubmit({ ip, dns_ptr: ptr.trim() });
  }
</script>

<Dialog bind:open title="Reverse DNS">
  <form onsubmit={submit}>
    <Field label="IP">
      {#snippet children(c)}
        <select id={c.id} bind:value={ip} onchange={chooseIP}>
          {#each ips as i (i)}<option value={i}>{i}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field label="Host name" hint="For example host.example.com; empty resets it to the default.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={ptr} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (open = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={ip === ""}>Save</Button>
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
