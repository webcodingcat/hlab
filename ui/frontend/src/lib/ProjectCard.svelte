<script lang="ts">
  import { tick } from "svelte";
  import { ChevronDown, Folder, Plus } from "@lucide/svelte";
  import ActionsMenu from "./ActionsMenu.svelte";
  import { resourceEntries, resourcesText, type Layout, type Project } from "./projects";
  import { consoleStart, href } from "./routes";

  // One project of the project list: an equal-size card in the grid or a
  // full-width row in the list, with the same content. A click on the card
  // outside its controls opens the project console.
  let { project, layout, ondelete }: { project: Project; layout: Layout; ondelete: (p: Project) => void } = $props();

  const entries = $derived(resourceEntries(project.resources_by_type ?? {}));
  const id = $props.id();

  // The resource menu of the footer.
  let open = $state(false);
  let footer: HTMLDivElement;
  let menu = $state<HTMLUListElement>();

  function items(): HTMLElement[] {
    return menu ? [...menu.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
  }

  async function toggle() {
    open = !open;
    if (open) {
      await tick();
      items()[0]?.focus();
    }
  }

  function onkeydown(e: KeyboardEvent) {
    const list = items();
    const i = list.indexOf(document.activeElement as HTMLElement);
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      open = false;
      footer.querySelector<HTMLElement>("button")?.focus();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      list[(i + step + list.length) % list.length]?.focus();
    } else if (e.key === "Tab") {
      open = false;
    }
  }

  // Controls inside the card keep their own click.
  function cardClick(e: MouseEvent) {
    if ((e.target as Element).closest("a, button, [role='menu']")) return;
    location.hash = href(consoleStart(project.name));
  }
</script>

<svelte:window onclick={(e) => open && !footer.contains(e.target as Node) && (open = false)} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class={["card", layout]} onclick={cardClick}>
  <div class="head">
    <Folder size={20} class="icon" aria-hidden="true" />
    <div class="names">
      <a class="display" href={href(consoleStart(project.name))}>{project.display_name}</a>
      <span class="name">{project.name}</span>
    </div>
    <div class="actions">
      <ActionsMenu items={[{ label: "Delete", danger: true, onclick: () => ondelete(project) }]} />
    </div>
  </div>
  <div class="footer" bind:this={footer}>
    {#if project.resources > 0}
      <button class="resources" aria-haspopup="menu" aria-expanded={open} aria-controls={`${id}-menu`} onclick={toggle}>
        {resourcesText(project.resources)}<ChevronDown size={16} aria-hidden="true" />
      </button>
      {#if open}
        <ul id={`${id}-menu`} role="menu" aria-label="Resources" bind:this={menu} {onkeydown}>
          {#each entries as e (e.key)}
            <li role="none">
              <a role="menuitem" href={href({ area: "project", project: project.name, view: e.view, page: { kind: "list" } })} onclick={() => (open = false)}>
                {e.label}
              </a>
            </li>
          {/each}
        </ul>
      {/if}
    {:else}
      <a class="create" href={href({ area: "project", project: project.name, view: "servers", page: { kind: "new" } })}>
        <Plus size={16} aria-hidden="true" />Create server
      </a>
    {/if}
  </div>
</div>

<style>
  .card {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: var(--s4);
    box-sizing: border-box;
    min-width: 0;
    height: 100%;
    min-height: 128px;
    padding: var(--s4);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    cursor: pointer;
  }
  .card:hover {
    border-color: var(--accent);
  }
  .card.list {
    flex-direction: row;
    align-items: center;
    width: 100%;
    height: auto;
    min-height: 0;
    padding: var(--s3) var(--s4);
  }
  .head {
    display: flex;
    align-items: flex-start;
    gap: var(--s3);
    min-width: 0;
  }
  /* In the list the parts of the head line up with the footer in one row. */
  .list .head {
    display: contents;
  }
  .card :global(.icon) {
    flex: none;
    color: var(--muted);
  }
  .names {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
  }
  .display {
    overflow: hidden;
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    color: var(--text);
    text-decoration: none;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .display:hover {
    text-decoration: underline;
  }
  .name {
    overflow: hidden;
    font-size: 12px;
    line-height: 16px;
    color: var(--muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .actions {
    flex: none;
  }
  .list .actions {
    order: 2;
  }
  .footer {
    position: relative;
    flex: none;
  }
  .list .footer {
    order: 1;
  }
  .resources,
  .create {
    display: inline-flex;
    align-items: center;
    gap: var(--s1);
    padding: var(--s1) var(--s2);
    margin-left: calc(-1 * var(--s2));
    border: 0;
    border-radius: var(--radius);
    background: none;
    color: var(--accent);
    font: inherit;
    text-decoration: none;
    cursor: pointer;
  }
  .resources:hover,
  .resources[aria-expanded="true"],
  .create:hover {
    background: var(--bg);
  }
  .display:focus-visible,
  .resources:focus-visible,
  .create:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  ul {
    position: absolute;
    left: 0;
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
    cursor: default;
  }
  [role="menuitem"] {
    display: block;
    padding: var(--s2) var(--s3);
    color: var(--text);
    text-decoration: none;
    white-space: nowrap;
  }
  [role="menuitem"]:hover,
  [role="menuitem"]:focus-visible {
    background: var(--bg);
  }
  [role="menuitem"]:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
