<script lang="ts">
  import { untrack } from "svelte";
  import Button from "./Button.svelte";
  import Field from "./Field.svelte";
  import type { Input } from "./resourceActions";

  // The input of a resource's Rename dialog: the new name, starting from the
  // current one; shared by every detail page with a Rename menu entry.
  let { current, submit, cancel }: { current: string; submit: (i: Input) => void; cancel: () => void } = $props();

  let name = $state(untrack(() => current));
  const invalid = $derived(name.trim() === "" || name.trim() === current);

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    submit({ name: name.trim() });
  }
</script>

<form onsubmit={send}>
  <Field label="Name">
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <div class="buttons">
    <Button onclick={cancel}>Cancel</Button>
    <Button type="submit" variant="primary" disabled={invalid}>Rename</Button>
  </div>
</form>

<style>
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
