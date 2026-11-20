<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import { LoaderCircle } from "@lucide/svelte";

  // icon: icon-only; give it a `label`, which becomes its accessible name.
  type Props = HTMLButtonAttributes & {
    variant?: "primary" | "secondary" | "danger" | "icon";
    loading?: boolean;
    label?: string;
    children?: Snippet;
  };

  let {
    variant = "secondary",
    loading = false,
    label,
    disabled,
    type = "button",
    children,
    ...rest
  }: Props = $props();
</script>

<button
  {...rest}
  {type}
  class={["btn", variant]}
  aria-label={label}
  title={variant === "icon" ? label : undefined}
  disabled={disabled || loading}
  aria-busy={loading || undefined}
>
  {#if loading}<LoaderCircle size={16} class="spin" aria-hidden="true" />{/if}
  {@render children?.()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--s2);
    min-height: 32px;
    padding: var(--s1) var(--s3);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    cursor: pointer;
    white-space: nowrap;
  }
  .btn:hover:not(:disabled) {
    filter: brightness(0.95);
  }
  .btn:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  .btn:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: #fff;
  }
  .danger {
    background: var(--error);
    border-color: var(--error);
    color: #fff;
  }
  .icon {
    padding: var(--s1);
    min-width: 32px;
    border-color: transparent;
    background: transparent;
  }
  .icon:hover:not(:disabled) {
    background: var(--border);
    filter: none;
  }
</style>
