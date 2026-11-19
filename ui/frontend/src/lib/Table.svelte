<script module lang="ts">
  export type Column<T> = {
    key: string;
    label: string;
    value: (row: T) => string | number;
    sortable?: boolean;
  };
  export type RowAction = { label: string; onclick: () => void };
</script>

<script lang="ts" generics="T">
  import type { Snippet } from "svelte";
  import { ArrowDown, ArrowUp, Ellipsis } from "@lucide/svelte";
  import Button from "./Button.svelte";

  // Resource table: rows undefined shows skeleton rows (loading). The search
  // matches the name, or with "=" a label selector (key=value, key!=value,
  // comma-separated). cell renders a column's content; default is its value.
  type Props = {
    title: string;
    rows: T[] | undefined;
    columns: Column<T>[];
    key: (row: T) => string | number;
    name: (row: T) => string;
    labels?: (row: T) => Record<string, string>;
    emptyText: string;
    createLabel?: string;
    oncreate?: () => void;
    onrowclick?: (row: T) => void;
    actions?: (row: T) => RowAction[];
    cell?: Snippet<[T, Column<T>]>;
  };

  let { title, rows, columns, key, name, labels, emptyText, createLabel, oncreate, onrowclick, actions, cell }: Props =
    $props();

  let query = $state("");
  let sortKey = $state<string | undefined>();
  let ascending = $state(true);
  let menuFor = $state<string | number | undefined>();

  function matches(row: T, q: string): boolean {
    if (!q.includes("=")) return name(row).toLowerCase().includes(q.toLowerCase());
    const l = labels?.(row) ?? {};
    return q.split(",").every((term) => {
      const [k, v] = term.split(/!?=/, 2).map((s) => s.trim());
      return term.includes("!=") ? l[k] !== v : l[k] === v;
    });
  }

  const shown = $derived.by(() => {
    if (!rows) return undefined;
    const q = query.trim();
    const list = q ? rows.filter((r) => matches(r, q)) : [...rows];
    const col = columns.find((c) => c.key === sortKey);
    if (col) {
      list.sort((a, b) => {
        const x = col.value(a);
        const y = col.value(b);
        const d = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
        return ascending ? d : -d;
      });
    }
    return list;
  });

  const empty = $derived(!!shown && shown.length === 0 && !query.trim());

  function sortBy(k: string) {
    ascending = sortKey === k ? !ascending : true;
    sortKey = k;
  }

  function toggleMenu(e: MouseEvent, k: string | number) {
    e.stopPropagation();
    menuFor = menuFor === k ? undefined : k;
  }
</script>

<svelte:window onclick={() => (menuFor = undefined)} onkeydown={(e) => e.key === "Escape" && (menuFor = undefined)} />

<section class="resource">
  <header>
    <h2>{title} <span class="muted">{rows ? rows.length : ""}</span></h2>
    <input type="search" aria-label="Search" placeholder="Search by name or key=value" bind:value={query} />
    <!-- The empty state carries the create button instead, so it shows once. -->
    {#if createLabel && oncreate && !empty}<Button variant="primary" onclick={oncreate}>{createLabel}</Button>{/if}
  </header>

  {#if empty}
    <div class="empty">
      <p>{emptyText}</p>
      {#if createLabel && oncreate}<Button variant="primary" onclick={oncreate}>{createLabel}</Button>{/if}
    </div>
  {:else}
    <table aria-busy={!shown}>
      <thead>
        <tr>
          {#each columns as c (c.key)}
            <th aria-sort={sortKey === c.key ? (ascending ? "ascending" : "descending") : undefined}>
              {#if c.sortable}
                <button class="sort" onclick={() => sortBy(c.key)}>
                  {c.label}
                  {#if sortKey === c.key}
                    {#if ascending}<ArrowUp size={14} aria-hidden="true" />{:else}<ArrowDown size={14} aria-hidden="true" />{/if}
                  {/if}
                </button>
              {:else}{c.label}{/if}
            </th>
          {/each}
          {#if actions}<th><span class="hidden">Actions</span></th>{/if}
        </tr>
      </thead>
      <tbody>
        {#if !shown}
          {#each [1, 2, 3] as i (i)}
            <tr class="skeleton">
              {#each columns as c (c.key)}<td><span class="bar"></span></td>{/each}
              {#if actions}<td></td>{/if}
            </tr>
          {/each}
        {:else if shown.length === 0}
          <tr><td colspan={columns.length + (actions ? 1 : 0)} class="muted">Nothing matches the search.</td></tr>
        {:else}
          {#each shown as row (key(row))}
            <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
            <tr
              class:clickable={!!onrowclick}
              tabindex={onrowclick ? 0 : undefined}
              onclick={() => onrowclick?.(row)}
              onkeydown={(e) => e.key === "Enter" && e.target === e.currentTarget && onrowclick?.(row)}
            >
              {#each columns as c (c.key)}
                <td>{#if cell}{@render cell(row, c)}{:else}{c.value(row)}{/if}</td>
              {/each}
              {#if actions}
                <td class="menu">
                  <Button variant="icon" label="Actions" aria-haspopup="menu" aria-expanded={menuFor === key(row)}
                    onclick={(e) => toggleMenu(e, key(row))}><Ellipsis size={16} aria-hidden="true" /></Button>
                  {#if menuFor === key(row)}
                    <ul role="menu">
                      {#each actions(row) as a (a.label)}
                        <li role="none">
                          <button role="menuitem" onclick={(e) => { e.stopPropagation(); menuFor = undefined; a.onclick(); }}>
                            {a.label}
                          </button>
                        </li>
                      {/each}
                    </ul>
                  {/if}
                </td>
              {/if}
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  {/if}
</section>

<style>
  header {
    display: flex;
    align-items: center;
    gap: var(--s3);
    margin-bottom: var(--s3);
  }
  header h2 {
    flex: 1;
  }
  input[type="search"] {
    width: 280px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  th,
  td {
    text-align: left;
    padding: var(--s2) var(--s3);
    border-bottom: 1px solid var(--border);
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 12px;
  }
  .sort {
    display: inline-flex;
    align-items: center;
    gap: var(--s1);
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .clickable {
    cursor: pointer;
  }
  .clickable:hover {
    background: var(--bg);
  }
  .bar {
    display: block;
    height: 12px;
    border-radius: var(--radius);
    background: var(--border);
  }
  .menu {
    position: relative;
    width: 48px;
  }
  ul {
    position: absolute;
    right: var(--s3);
    top: 100%;
    z-index: 10;
    min-width: 160px;
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
    cursor: pointer;
  }
  [role="menuitem"]:hover,
  [role="menuitem"]:focus-visible {
    background: var(--bg);
  }
  .empty {
    padding: var(--s8);
    text-align: center;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
</style>
