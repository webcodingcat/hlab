<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import type { Zone } from "./Section.svelte";

  // The input of a zone action dialog: Change default TTL asks for the TTL,
  // Import zone file for the zone file text, Change protection for the
  // delete flag. It is created anew each time a dialog opens, so it starts
  // from the zone's current values.
  type Props = { action: ResourceAction; zone: Zone; submit: (i: Input) => void; cancel: () => void };

  let { action, zone, submit, cancel }: Props = $props();

  let ttl = $state<number | null>(untrack(() => zone.ttl));
  let zonefile = $state("");
  let protectDelete = $state(untrack(() => zone.protection.delete));

  const invalid = $derived(
    (action.id === "change_ttl" && (ttl == null || ttl < 60)) || (action.id === "import_zonefile" && zonefile.trim() === ""),
  );

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    if (action.id === "change_ttl") submit({ ttl });
    else if (action.id === "import_zonefile") submit({ zonefile });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "change_ttl"}
    <Field label="TTL" hint="The default TTL of the zone's records in seconds, at least 60.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} type="number" min="60" bind:value={ttl} />
      {/snippet}
    </Field>
  {:else if action.id === "import_zonefile"}
    <Field label="Zone file" hint="The zone file in BIND format; it replaces all records of the zone.">
      {#snippet children(c)}
        <textarea id={c.id} aria-describedby={c.describedby} rows="12" spellcheck="false" bind:value={zonefile}></textarea>
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
  input:not([type="checkbox"]),
  textarea {
    width: 100%;
  }
  textarea {
    font-family: var(--mono);
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
