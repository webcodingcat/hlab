<script lang="ts">
  import { TriangleAlert } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import CopyField from "./CopyField.svelte";
  import Dialog from "./Dialog.svelte";

  // Shows a one-time secret (a root password) with a copy button and the
  // warning that it is not shown again. The value is only rendered while
  // the dialog is open; the caller drops it when open turns false.
  type Props = { open: boolean; title: string; label: string; value: string | undefined };

  let { open = $bindable(), title, label, value }: Props = $props();
</script>

<Dialog bind:open {title}>
  {#if open && value !== undefined}
    <p class="warning">
      <TriangleAlert size={16} aria-hidden="true" />
      Copy the {label} now. It is not shown again.
    </p>
    <CopyField {value} {label} />
  {/if}
  <div class="actions">
    <Button variant="primary" onclick={() => (open = false)}>Close</Button>
  </div>
</Dialog>

<style>
  .warning {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s3);
    color: var(--warning);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    margin-top: var(--s4);
  }
</style>
