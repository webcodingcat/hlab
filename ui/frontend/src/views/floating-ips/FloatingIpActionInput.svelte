<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import type { FloatingIp } from "./Section.svelte";

  // The input of a floating IP action dialog: Assign asks for a server in
  // the network zone of the home location, Change protection for the delete
  // flag. It is created anew each time a dialog opens, so it starts from
  // the floating IP's current values.
  type Server = { id: number; name: string; location: { name: string; network_zone: string } };
  type Props = {
    action: ResourceAction;
    fip: FloatingIp;
    servers: Server[];
    submit: (i: Input) => void;
    cancel: () => void;
  };

  let { action, fip, servers, submit, cancel }: Props = $props();

  let server = $state("");
  let protectDelete = $state(untrack(() => fip.protection.delete));

  const candidates = $derived(
    servers.filter((s) => s.location.network_zone === fip.home_location.network_zone && s.id !== fip.server),
  );
  const invalid = $derived(action.id === "assign" && server === "");

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    if (action.id === "assign") submit({ server: Number(server) });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "assign"}
    <Field label="Server" hint={`Servers in the network zone ${fip.home_location.network_zone}. Assigning moves the IP.`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={server}>
          <option value="" disabled>Choose a server</option>
          {#each candidates as s (s.id)}<option value={`${s.id}`}>{s.name} ({s.location.name})</option>{/each}
        </select>
      {/snippet}
    </Field>
  {:else}
    <label class="check"><input type="checkbox" bind:checked={protectDelete} />Protect against deletion</label>
  {/if}
  <div class="buttons">
    <Button onclick={cancel}>Cancel</Button>
    <Button type="submit" variant="primary" disabled={invalid}>{action.label}</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
  }
  select {
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
</style>
