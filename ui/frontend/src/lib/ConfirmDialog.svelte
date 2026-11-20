<script lang="ts">
  import type { Snippet } from "svelte";
  import Button from "./Button.svelte";

  // Confirms a delete or another destructive action. With typedName the
  // confirm button stays disabled until that name is typed. onconfirm runs
  // with the button loading; the dialog closes when it resolves. Escape
  // and Cancel close it without running anything.
  type Props = {
    open: boolean;
    title: string;
    confirmLabel: string;
    typedName?: string;
    onconfirm: () => unknown;
    children?: Snippet;
  };

  let { open = $bindable(), title, confirmLabel, typedName, onconfirm, children }: Props = $props();

  let dialog: HTMLDialogElement;
  let typed = $state("");
  let busy = $state(false);
  const inputId = $props.id();

  const ready = $derived(typedName === undefined || typed === typedName);

  $effect(() => {
    if (open && !dialog.open) {
      typed = "";
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  async function confirm(e: SubmitEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    busy = true;
    try {
      await onconfirm();
    } finally {
      busy = false;
      open = false;
    }
  }
</script>

<dialog bind:this={dialog} onclose={() => (open = false)} aria-labelledby={`${inputId}-title`}>
  <form onsubmit={confirm}>
    <h2 id={`${inputId}-title`}>{title}</h2>
    <div class="body">{@render children?.()}</div>
    {#if typedName !== undefined}
      <label for={inputId}>Type <strong>{typedName}</strong> to confirm</label>
      <input id={inputId} type="text" autocomplete="off" bind:value={typed} />
    {/if}
    <div class="actions">
      <Button onclick={() => (open = false)} disabled={busy}>Cancel</Button>
      <Button type="submit" variant="danger" loading={busy} disabled={!ready}>{confirmLabel}</Button>
    </div>
  </form>
</dialog>

<style>
  dialog {
    width: 440px;
    padding: var(--s6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.4);
  }
  form {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
  }
  input {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s2);
  }
</style>
