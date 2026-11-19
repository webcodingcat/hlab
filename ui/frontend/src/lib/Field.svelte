<script lang="ts">
  import type { Snippet } from "svelte";

  // The control comes as a snippet and takes the given id, aria-describedby
  // and aria-invalid, so the label and the messages belong to it.
  type Control = { id: string; describedby: string | undefined; invalid: boolean };
  type Props = { label: string; error?: string; hint?: string; children: Snippet<[Control]> };

  let { label, error, hint, children }: Props = $props();

  const id = $props.id();
  const describedby = $derived(
    [error ? `${id}-error` : "", hint ? `${id}-hint` : ""].filter(Boolean).join(" ") || undefined,
  );
</script>

<div class="field">
  <label for={id}>{label}</label>
  {@render children({ id, describedby, invalid: !!error })}
  {#if hint}<div id={`${id}-hint`} class="hint">{hint}</div>{/if}
  {#if error}<div id={`${id}-error`} class="error">{error}</div>{/if}
</div>

<style>
  .field {
    display: flex;
    flex-direction: column;
    gap: var(--s1);
    margin-bottom: var(--s4);
  }
  label {
    font-weight: 600;
  }
  .hint,
  .error {
    font-size: 12px;
    line-height: 16px;
  }
  .hint {
    color: var(--muted);
  }
  .error {
    color: var(--error);
  }
</style>
