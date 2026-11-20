<script lang="ts">
  import type { Snippet } from "svelte";

  // A modal dialog on the native <dialog> element. open shows and closes it;
  // Escape closes it too, and a "Cancel" button in children closes it by
  // setting open to false. onclose runs on every close, whatever closed it.
  type Props = {
    open: boolean;
    title: string;
    onclose?: () => void;
    children: Snippet;
  };

  let { open = $bindable(), title, onclose, children }: Props = $props();

  let dialog: HTMLDialogElement;
  const id = $props.id();

  $effect(() => {
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  });

  function closed() {
    open = false;
    onclose?.();
  }
</script>

<dialog bind:this={dialog} onclose={closed} aria-labelledby={`${id}-title`}>
  <h2 id={`${id}-title`}>{title}</h2>
  {@render children()}
</dialog>

<style>
  dialog {
    width: 480px;
    padding: var(--s6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.4);
  }
  h2 {
    margin-bottom: var(--s4);
  }
</style>
