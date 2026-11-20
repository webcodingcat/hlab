<script lang="ts">
  import type { HTMLAttributes } from "svelte/elements";

  // tone by role: success (running), warning (starting), muted (off), error.
  type Props = HTMLAttributes<HTMLSpanElement> & {
    tone: "success" | "warning" | "muted" | "error";
    text: string;
  };

  let { tone, text, ...rest }: Props = $props();
</script>

<span {...rest} class={["pill", tone]}><span class="dot" aria-hidden="true"></span>{text}</span>

<style>
  .pill {
    display: inline-flex;
    align-items: center;
    gap: var(--s2);
    padding: 2px var(--s2);
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface);
    font-size: 12px;
    line-height: 16px;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: currentColor;
  }
  .success .dot {
    background: var(--success);
  }
  .warning .dot {
    background: var(--warning);
  }
  .muted .dot {
    background: var(--muted);
  }
  .error .dot {
    background: var(--error);
  }
</style>
