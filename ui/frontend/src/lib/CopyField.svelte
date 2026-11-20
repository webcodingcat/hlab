<script lang="ts">
  import { Check, Copy } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import { showError } from "./toast";

  // label names what is copied, e.g. "token"; the button reads "Copy token".
  let { value, label = "value" }: { value: string; label?: string } = $props();

  let copied = $state(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch (err) {
      showError(`Copy ${label}`, err);
    }
  }
</script>

<div class="copy">
  <code>{value}</code>
  <Button variant="icon" label={`Copy ${label}`} onclick={copy}>
    {#if copied}<Check size={16} aria-hidden="true" />{:else}<Copy size={16} aria-hidden="true" />{/if}
  </Button>
</div>

<style>
  .copy {
    display: flex;
    align-items: center;
    gap: var(--s2);
    padding: var(--s1) var(--s1) var(--s1) var(--s2);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
  }
  code {
    flex: 1;
    overflow-wrap: anywhere;
    user-select: all;
  }
</style>
