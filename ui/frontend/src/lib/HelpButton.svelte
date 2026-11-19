<script lang="ts">
  import { X } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import { helpFor, type ViewId } from "../help";
  import { helpTexts } from "../helpTexts";

  // The `?` button of a view's title row; opens the view's help text in a
  // side panel. Escape and the close button close it.
  let { view }: { view: ViewId } = $props();

  let open = $state(false);
  const html = $derived(helpFor(view, helpTexts));
  const panelId = $props.id();

  function onkeydown(e: KeyboardEvent) {
    if (open && e.key === "Escape") open = false;
  }
</script>

<svelte:window {onkeydown} />

{#if html !== undefined}
  <Button
    variant="icon"
    label="Help"
    aria-expanded={open}
    aria-controls={panelId}
    onclick={() => (open = !open)}><span class="mark" aria-hidden="true">?</span></Button
  >
{/if}

{#if open && html !== undefined}
  <aside id={panelId} class="panel" aria-label="Help">
    <div class="close">
      <Button variant="icon" label="Close help" onclick={() => (open = false)}><X size={16} aria-hidden="true" /></Button>
    </div>
    <div class="text">
      {@html html}
    </div>
  </aside>
{/if}

<style>
  .mark {
    font-weight: 600;
    width: 16px;
  }
  .panel {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 10;
    width: 360px;
    overflow-y: auto;
    padding: var(--s4) var(--s6);
    border-left: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    box-shadow: -4px 0 16px rgb(0 0 0 / 0.12);
  }
  .close {
    display: flex;
    justify-content: flex-end;
  }
  .text :global(h1) {
    margin: 0 0 var(--s3);
    font-size: 20px;
    font-weight: 600;
  }
  .text :global(h2),
  .text :global(h3) {
    margin: var(--s4) 0 var(--s2);
    font-size: 16px;
    font-weight: 600;
  }
  .text :global(p),
  .text :global(ul) {
    margin: 0 0 var(--s3);
  }
  .text :global(ul) {
    padding-left: var(--s6);
  }
  .text :global(code),
  .text :global(pre) {
    font-family: var(--mono);
  }
  .text :global(pre) {
    padding: var(--s2) var(--s3);
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .text :global(a) {
    color: var(--accent);
  }
</style>
