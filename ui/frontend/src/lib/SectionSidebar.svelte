<script lang="ts" module>
  import type { Cpu } from "@lucide/svelte";

  /** SectionItem is one link of a section sidebar; separator draws a line above it. */
  export type SectionItem = { href: string; title: string; icon: typeof Cpu; current: boolean; separator?: boolean };
</script>

<script lang="ts">
  // The left sidebar of an area (a project console, the Administration):
  // one link per section, the current one marked.
  let { label, items }: { label: string; items: SectionItem[] } = $props();
</script>

<aside class="sidebar">
  <nav aria-label={label}>
    {#each items as item (item.href)}
      {@const Icon = item.icon}
      {#if item.separator}<hr />{/if}
      <a href={item.href} aria-current={item.current ? "page" : undefined}>
        <Icon size={20} aria-hidden="true" />{item.title}
      </a>
    {/each}
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
</style>
