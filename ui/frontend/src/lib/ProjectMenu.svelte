<script lang="ts">
  import { ChevronDown } from "@lucide/svelte";
  import { mgmt } from "./bridge";
  import { showError } from "./toast";
  import { consoleStart, href, running, type Project } from "../state";

  // The project name in the breadcrumb of a project console; it opens a
  // menu with the other projects to switch to.
  let { project }: { project: string } = $props();

  let open = $state(false);
  let others = $state<string[] | undefined>();

  async function load() {
    others = undefined;
    if (!$running) {
      others = [];
      return;
    }
    try {
      const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
      others = (res.projects ?? []).map((p) => p.name).filter((n) => n !== project);
    } catch (err) {
      others = [];
      showError("Load projects", err);
    }
  }

  function toggle(e: MouseEvent) {
    e.stopPropagation();
    open = !open;
    if (open) load();
  }
</script>

<svelte:window onclick={() => (open = false)} onkeydown={(e) => e.key === "Escape" && (open = false)} />

<div class="wrap">
  <button class="name" aria-haspopup="menu" aria-expanded={open} aria-current="page" onclick={toggle}>
    {project}<ChevronDown size={16} aria-hidden="true" />
  </button>
  {#if open}
    <ul role="menu" aria-label="Switch project">
      {#if !others}
        <li role="none" class="muted">Loading…</li>
      {:else}
        {#each others as name (name)}
          <li role="none">
            <a role="menuitem" href={href(consoleStart(name))} onclick={() => (open = false)}>{name}</a>
          </li>
        {:else}
          <li role="none" class="muted">No other projects</li>
        {/each}
      {/if}
    </ul>
  {/if}
</div>

<style>
  .wrap {
    position: relative;
  }
  .name {
    display: inline-flex;
    align-items: center;
    gap: var(--s1);
    padding: var(--s1) var(--s2);
    border: 0;
    border-radius: var(--radius);
    background: none;
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .name:hover,
  .name[aria-expanded="true"] {
    background: var(--bg);
  }
  ul {
    position: absolute;
    left: 0;
    top: 100%;
    z-index: 10;
    min-width: 200px;
    max-height: 320px;
    overflow-y: auto;
    margin: 0;
    padding: var(--s1) 0;
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
  }
  li.muted {
    padding: var(--s2) var(--s3);
  }
  [role="menuitem"] {
    display: block;
    padding: var(--s2) var(--s3);
    color: var(--text);
    text-decoration: none;
  }
  [role="menuitem"]:hover,
  [role="menuitem"]:focus-visible {
    background: var(--bg);
  }
</style>
