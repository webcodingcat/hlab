<script lang="ts">
  import type { Snippet } from "svelte";
  import Button from "./Button.svelte";
  import HelpButton from "./HelpButton.svelte";
  import type { ViewId } from "../help";

  // A create form on its own page: title row with help button, the fields
  // in one column, the general error line, Cancel and submit at the bottom
  // right. Enter submits; busy shows the spinner in the submit button;
  // disabled keeps the form from being sent.
  type Props = {
    title: string;
    help: ViewId;
    submitLabel: string;
    busy: boolean;
    disabled?: boolean;
    general?: string;
    onsubmit: () => void;
    oncancel: () => void;
    children: Snippet;
  };

  let { title, help, submitLabel, busy, disabled = false, general, onsubmit, oncancel, children }: Props = $props();

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!busy && !disabled) onsubmit();
  }
</script>

<div class="title-row">
  <h1>{title}</h1>
  <HelpButton view={help} />
</div>

<form onsubmit={submit}>
  {@render children()}
  {#if general}<p class="general" role="alert">{general}</p>{/if}
  <div class="actions">
    <Button onclick={oncancel} disabled={busy}>Cancel</Button>
    <Button type="submit" variant="primary" loading={busy} {disabled}>{submitLabel}</Button>
  </div>
</form>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  form {
    max-width: 640px;
    padding: var(--s6);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .general {
    margin-bottom: var(--s3);
    color: var(--error);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
