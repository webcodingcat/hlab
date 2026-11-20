<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import type { Image } from "./ImageList.svelte";

  // The input of an image action dialog: Edit description asks for the new
  // description, Change protection for the delete flag. It is created anew
  // each time a dialog opens, so it starts from the image's current values.
  type Props = { action: ResourceAction; image: Image; submit: (i: Input) => void; cancel: () => void };

  let { action, image, submit, cancel }: Props = $props();

  let description = $state(untrack(() => image.description));
  let protectDelete = $state(untrack(() => image.protection.delete));

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (action.id === "description") submit({ description: description.trim() });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "description"}
    <Field label="Description">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={description} />
      {/snippet}
    </Field>
  {:else}
    <label class="check"><input type="checkbox" bind:checked={protectDelete} />Protect against deletion</label>
  {/if}
  <div class="buttons">
    <Button onclick={cancel}>Cancel</Button>
    <Button type="submit" variant="primary">{action.label}</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
  }
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
</style>
