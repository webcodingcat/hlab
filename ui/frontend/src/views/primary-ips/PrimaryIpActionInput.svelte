<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { assignable, type PrimaryIp, type Server } from "./Section.svelte";

  // The input of a primary IP action dialog: Assign asks for a powered-off
  // server of the IP's location, Change protection for the delete flag. It
  // is created anew each time a dialog opens, so it starts from the primary
  // IP's current values.
  type Props = {
    action: ResourceAction;
    pip: PrimaryIp;
    servers: Server[];
    submit: (i: Input) => void;
    cancel: () => void;
  };

  let { action, pip, servers, submit, cancel }: Props = $props();

  let server = $state("");
  let protectDelete = $state(untrack(() => pip.protection.delete));

  const candidates = $derived(assignable(servers, pip.location.name, pip.type));
  const invalid = $derived(action.id === "assign" && server === "");

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    if (action.id === "assign") submit({ assignee_id: Number(server) });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "assign"}
    <Field label="Server" hint={`Powered-off servers in ${pip.location.name} without a public ${pip.type === "ipv6" ? "IPv6" : "IPv4"}.`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={server}>
          <option value="" disabled>Choose a server</option>
          {#each candidates as s (s.id)}<option value={`${s.id}`}>{s.name}</option>{/each}
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
