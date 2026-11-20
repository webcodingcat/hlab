<script lang="ts" module>
  import type { Cpu } from "@lucide/svelte";

  /** SectionItem is one link of a section sidebar; separator draws a line above it. */
  export type SectionItem = { href: string; title: string; icon: typeof Cpu; current: boolean; separator?: boolean };

  /** SectionGroup is a group of links; a group with a title can be collapsed. */
  export type SectionGroup = { id: string; title?: string; items: SectionItem[] };
</script>

<script lang="ts">
  import { ChevronDown } from "@lucide/svelte";
  import { collapsedGroups } from "../state";

  // The left sidebar of an area (a project console, the Administration):
  // one link per section, the current one marked. With groups, a line
  // separates the groups and a click on a group's heading collapses or
  // expands it; the collapsed groups are remembered.
  let { label, items = [], groups }: { label: string; items?: SectionItem[]; groups?: SectionGroup[] } = $props();

  function toggle(id: string) {
    collapsedGroups.update((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]));
  }
</script>

{#snippet link(item: SectionItem)}
  {@const Icon = item.icon}
  <a href={item.href} aria-current={item.current ? "page" : undefined}>
    <Icon size={20} aria-hidden="true" />{item.title}
  </a>
{/snippet}

<aside class="sidebar">
  <nav aria-label={label}>
    {#if groups}
      {#each groups as group, i (group.id)}
        {@const collapsed = group.title !== undefined && $collapsedGroups.includes(group.id)}
        {#if i > 0}<hr />{/if}
        {#if group.title !== undefined}
          <button type="button" class="heading" aria-expanded={!collapsed} onclick={() => toggle(group.id)}>
            <span>{group.title}</span><ChevronDown size={16} aria-hidden="true" class={collapsed ? "closed" : ""} />
          </button>
        {/if}
        {#if !collapsed}
          {#each group.items as item (item.href)}{@render link(item)}{/each}
        {/if}
      {/each}
    {:else}
      {#each items as item (item.href)}
        {#if item.separator}<hr />{/if}
        {@render link(item)}
      {/each}
    {/if}
  </nav>
</aside>

<style>
  .sidebar {
    flex: none;
    width: 220px;
    padding: var(--s4) var(--s3);
    border-right: 1px solid var(--border);
    background: var(--surface);
  }
  nav {
    display: flex;
    flex-direction: column;
    gap: var(--s1);
  }
  a {
    display: flex;
    align-items: center;
    gap: var(--s2);
    padding: var(--s2);
    border-radius: var(--radius);
    color: var(--text);
    text-decoration: none;
  }
  hr {
    width: 100%;
    margin: var(--s2) 0;
    border: 0;
    border-top: 1px solid var(--border);
  }
  a:hover {
    background: var(--bg);
  }
  a[aria-current="page"] {
    background: var(--bg);
    color: var(--accent);
    font-weight: 600;
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--s1) var(--s2);
    border: 0;
    border-radius: var(--radius);
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    line-height: 16px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    cursor: pointer;
  }
  .heading:hover {
    color: var(--text);
  }
  .heading:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .heading :global(.closed) {
    transform: rotate(-90deg);
  }
</style>
