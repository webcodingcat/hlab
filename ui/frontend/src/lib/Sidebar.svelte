<script lang="ts">
  import { Cpu, FolderKanban, ScrollText, Settings } from "@lucide/svelte";
  import { loadProjects, project, projects, running, view, views, type ViewId } from "../state";

  // Project switcher on top (needs the running cloud), then the app sections.
  const icons: Record<ViewId, typeof Cpu> = {
    projects: FolderKanban,
    "server-types": Cpu,
    settings: Settings,
    logs: ScrollText,
  };

  $effect(() => {
    if ($running) loadProjects();
  });
</script>

<aside class="sidebar">
  <div class="switcher">
    <label for="project-switcher" class="small muted">Project</label>
    <select id="project-switcher" bind:value={$project} disabled={!$running || $projects.length === 0}>
      {#if $projects.length === 0}
        <option value={$project}>{$running ? "No projects" : "Start the cloud to choose"}</option>
      {/if}
      {#each $projects as p (p.name)}
        <option value={p.name}>{p.name}</option>
      {/each}
    </select>
  </div>
  <hr />
  <nav aria-label="App sections">
    {#each views as v (v.id)}
      {@const Icon = icons[v.id]}
      <a href={`#/${v.id}`} aria-current={$view === v.id ? "page" : undefined}>
        <Icon size={20} aria-hidden="true" />{v.title}
      </a>
    {/each}
  </nav>
</aside>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    width: 220px;
    padding: var(--s4) var(--s3);
    border-right: 1px solid var(--border);
    background: var(--surface);
  }
  .switcher {
    display: flex;
    flex-direction: column;
    gap: var(--s1);
  }
  select {
    width: 100%;
  }
  hr {
    width: 100%;
    margin: 0;
    border: 0;
    border-top: 1px solid var(--border);
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
  a:hover {
    background: var(--bg);
  }
  a[aria-current="page"] {
    background: var(--bg);
    color: var(--accent);
    font-weight: 600;
  }
</style>
