<script lang="ts">
  import { ChevronDown } from "@lucide/svelte";
  import { mgmt } from "./bridge";
  import { showError } from "./toast";
  import { sortProjects } from "./projects";
  import { consoleStart, href, running, type Project } from "../state";

  // The project in the breadcrumb of a project console, shown by its display
  // name; it opens a menu with the other projects to switch to.
  let { project }: { project: string } = $props();

  let open = $state(false);
  let projects = $state<Project[] | undefined>();

  const label = $derived(projects?.find((p) => p.name === project)?.display_name || project);
  const others = $derived(
    projects &&
      sortProjects(
        projects.filter((p) => p.name !== project),
        { key: "display_name", dir: "asc" },
      ),
  );

  async function load(isRunning: boolean) {
    if (!isRunning) {
      projects = [];
      return;
    }
    try {
      const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
      projects = res.projects ?? [];
    } catch (err) {
      projects = [];
      showError("Load projects", err);
    }
  }

  // Loads when mounted, when project changes and when the cloud starts.
  $effect(() => {
    void project;
    load($running);
  });

  function toggle(e: MouseEvent) {
    e.stopPropagation();
    open = !open;
    if (open) load($running);
  }
</script>

<svelte:window onclick={() => (open = false)} onkeydown={(e) => e.key === "Escape" && (open = false)} />

<div class="wrap">
  <button class="name" aria-haspopup="menu" aria-expanded={open} aria-current="page" onclick={toggle}>
    {label}<ChevronDown size={16} aria-hidden="true" />
  </button>
  {#if open}
    <ul role="menu" aria-label="Switch project">
      {#if !others}
        <li role="none" class="muted">Loading…</li>
      {:else}
        {#each others as p (p.name)}
          <li role="none">
            <a role="menuitem" href={href(consoleStart(p.name))} onclick={() => (open = false)}>{p.display_name}</a>
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
