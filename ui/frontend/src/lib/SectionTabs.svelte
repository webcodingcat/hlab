<script lang="ts">
  import { entryOf } from "./consoleNav";
  import { href, type ProjectView } from "./routes";

  // The tab bar on top of every page of a project console entry: one link
  // per tab of the entry that holds view, the open one underlined.
  let { project, view }: { project: string; view: ProjectView } = $props();

  const tabs = $derived(entryOf(view).tabs);
</script>

<nav aria-label="Section">
  {#each tabs as t (t.view)}
    <a
      href={href({ area: "project", project, view: t.view, page: { kind: "list" } })}
      aria-current={t.view === view ? "page" : undefined}>{t.title}</a
    >
  {/each}
</nav>

<style>
  nav {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s1);
    margin-bottom: var(--s4);
    border-bottom: 1px solid var(--border);
  }
  a {
    padding: var(--s2) var(--s3);
    margin-bottom: -1px;
    border-bottom: 2px solid transparent;
    color: var(--muted);
    text-decoration: none;
  }
  a:hover {
    color: var(--text);
  }
  a:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  a[aria-current="page"] {
    border-bottom-color: var(--accent);
    color: var(--text);
    font-weight: 600;
  }
</style>
