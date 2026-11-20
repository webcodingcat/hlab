<script module lang="ts">
  export type MenuItem = { label: string; onclick?: () => void; danger?: boolean; disabled?: boolean; hint?: string };
</script>

<script lang="ts">
  import { tick } from "svelte";
  import { Ellipsis } from "@lucide/svelte";
  import Button from "./Button.svelte";

  // A "…" button labelled "Actions" opening a menu. Arrow keys move between
  // the items, Escape closes the menu and returns to the button. A disabled
  // item stays focusable, shows its hint as text and never runs.
  let { items }: { items: MenuItem[] } = $props();

  let open = $state(false);
  let root: HTMLDivElement;
  let menu = $state<HTMLUListElement>();
  const id = $props.id();

  function entries(): HTMLElement[] {
    return menu ? [...menu.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
  }

  async function toggle() {
    open = !open;
    if (open) {
      await tick();
      entries()[0]?.focus();
    }
  }

  function close(focusButton: boolean) {
    open = false;
    if (focusButton) root.querySelector<HTMLElement>("button")?.focus();
  }

  function onkeydown(e: KeyboardEvent) {
    const list = entries();
    const i = list.indexOf(document.activeElement as HTMLElement);
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      list[(i + step + list.length) % list.length]?.focus();
    } else if (e.key === "Tab") {
      open = false;
    }
  }

  function run(item: MenuItem) {
    if (item.disabled) return;
    close(false);
    item.onclick?.();
  }
</script>

<svelte:window onclick={(e) => open && !root.contains(e.target as Node) && (open = false)} />

<div class="wrap" bind:this={root}>
  <Button variant="icon" label="Actions" aria-haspopup="menu" aria-expanded={open} onclick={toggle}>
    <Ellipsis size={16} aria-hidden="true" />
  </Button>
  {#if open}
    <ul role="menu" aria-label="Actions" bind:this={menu} {onkeydown}>
      {#each items as item, i (item.label)}
        <li role="none">
          <button
            role="menuitem"
            class:danger={item.danger}
            aria-disabled={item.disabled || undefined}
            aria-describedby={item.disabled && item.hint ? `${id}-hint-${i}` : undefined}
            onclick={() => run(item)}
          >
            {item.label}
          </button>
          {#if item.disabled && item.hint}<span id={`${id}-hint-${i}`} class="hint">{item.hint}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .wrap {
    position: relative;
    display: inline-block;
  }
  ul {
    position: absolute;
    right: 0;
    top: 100%;
    z-index: 10;
    min-width: 200px;
    margin: 0;
    padding: var(--s1) 0;
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
  }
  [role="menuitem"] {
    display: block;
    width: 100%;
    padding: var(--s2) var(--s3);
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    text-align: left;
    white-space: nowrap;
    cursor: pointer;
  }
  [role="menuitem"]:hover,
  [role="menuitem"]:focus-visible {
    background: var(--bg);
  }
  [role="menuitem"]:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .danger {
    color: var(--error);
  }
  [aria-disabled="true"] {
    color: var(--muted);
    cursor: default;
  }
  .hint {
    display: block;
    padding: 0 var(--s3) var(--s2);
    font-size: 12px;
    line-height: 16px;
    color: var(--muted);
  }
</style>
