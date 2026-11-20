<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import type { Volume } from "./Section.svelte";

  // The input of a volume action dialog: Resize asks for a larger size,
  // Attach for a server of the volume's location and the mount option,
  // Change protection for the delete flag. It is created anew each time a
  // dialog opens, so it starts from the volume's current values.
  type Server = { id: number; name: string; location: { name: string } };
  type Props = {
    action: ResourceAction;
    volume: Volume;
    servers: Server[];
    submit: (i: Input) => void;
    cancel: () => void;
  };

  let { action, volume, servers, submit, cancel }: Props = $props();

  let size = $state(untrack(() => volume.size + 1));
  let server = $state("");
  let automount = $state(false);
  let protectDelete = $state(untrack(() => volume.protection.delete));

  const candidates = $derived(servers.filter((s) => s.location.name === volume.location.name));
  const invalid = $derived(
    (action.id === "resize" && (!Number.isInteger(size) || size <= volume.size || size > 10240)) ||
      (action.id === "attach" && server === ""),
  );

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    if (action.id === "resize") submit({ size });
    else if (action.id === "attach") submit({ server: Number(server), automount });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "resize"}
    <Field label="Size" hint={`More than ${volume.size} GB, up to 10240 GB. A volume cannot shrink.`}>
      {#snippet children(c)}
        <input
          id={c.id}
          aria-describedby={c.describedby}
          type="number"
          min={volume.size + 1}
          max="10240"
          step="1"
          bind:value={size}
        />
      {/snippet}
    </Field>
  {:else if action.id === "attach"}
    <Field label="Server" hint={`Servers in ${volume.location.name}.`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={server}>
          <option value="" disabled>Choose a server</option>
          {#each candidates as s (s.id)}<option value={`${s.id}`}>{s.name}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <label class="check"><input type="checkbox" bind:checked={automount} />Mount automatically</label>
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
  select,
  input[type="number"] {
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
