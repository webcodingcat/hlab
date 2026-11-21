<script lang="ts">
  import { tick } from "svelte";
  import { ArrowDown, ArrowUp, ChevronDown, LayoutGrid, List as ListIcon, Plus } from "@lucide/svelte";
  import Button from "../lib/Button.svelte";
  import CloudStopped from "../lib/CloudStopped.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import ProjectCard from "../lib/ProjectCard.svelte";
  import { ApiError, mgmt, service } from "../lib/bridge";
  import { actionTitle } from "../lib/format";
  import { filterProjects, sortProjects, type SortKey } from "../lib/projects";
  import { action, showError } from "../lib/toast";
  import { projectLayout, projectSort, running, type Project } from "../state";

  // The project list as cards (grid) or full-width rows (list), with search,
  // sort menu and layout switch; the last tile creates a project.
  let rows = $state<Project[] | undefined>();
  let failing = false;
  let query = $state("");

  const shown = $derived(rows && sortProjects(filterProjects(rows, query), $projectSort));

  async function load() {
    try {
      const res = await mgmt<{ projects: Project[] }>("GET", "/projects");
      rows = res.projects ?? [];
      failing = false;
    } catch (err) {
      if (!failing) showError("Load projects", err);
      failing = true;
    }
  }

  // Lists refresh every 5 seconds while visible and the cloud runs.
  $effect(() => {
    if (!$running) return;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Sort menu.
  const sortLabels: Record<SortKey, string> = { display_name: "Display name", created: "Created" };
  const sortKeys: SortKey[] = ["display_name", "created"];
  let sortOpen = $state(false);
  let sortRoot = $state<HTMLDivElement>();
  let sortMenu = $state<HTMLUListElement>();

  function sortItems(): HTMLElement[] {
    return sortMenu ? [...sortMenu.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
  }

  async function toggleSort() {
    sortOpen = !sortOpen;
    if (sortOpen) {
      await tick();
      sortItems()[0]?.focus();
    }
  }

  function sortKeydown(e: KeyboardEvent) {
    const list = sortItems();
    const i = list.indexOf(document.activeElement as HTMLElement);
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      sortOpen = false;
      sortRoot?.querySelector<HTMLElement>("button")?.focus();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      list[(i + step + list.length) % list.length]?.focus();
    } else if (e.key === "Tab") {
      sortOpen = false;
    }
  }

  function pickSort(key: SortKey) {
    sortOpen = false;
    projectSort.update((s) => ({ ...s, key }));
  }

  function reverse() {
    projectSort.update((s) => ({ ...s, dir: s.dir === "asc" ? "desc" : "asc" }));
  }

  // Create dialog: the derived name is shown live below the field.
  let createDialog: HTMLDialogElement;
  let newName = $state("");
  let previewName = $state("");
  let nameError = $state<string | undefined>();
  let creating = $state(false);
  let preview = 0;

  $effect(() => {
    const n = ++preview;
    service<string>("Service", "ProjectName", newName).then(
      (name) => {
        if (n === preview) previewName = name;
      },
      () => {
        if (n === preview) previewName = "";
      },
    );
  });

  function openCreate() {
    newName = "";
    nameError = undefined;
    createDialog.showModal();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (creating) return;
    creating = true;
    nameError = undefined;
    try {
      await mgmt("POST", "/projects", { display_name: newName });
      createDialog.close();
      await load();
    } catch (err) {
      if (err instanceof ApiError && (err.code === "invalid_input" || err.code === "conflict")) {
        nameError = err.fields?.display_name?.join(" ") || err.message;
      } else {
        showError(actionTitle("Project", newName.trim(), "Create"), err);
      }
    } finally {
      creating = false;
    }
  }

  // Delete: the display name must be typed; the deletion runs by name as an action.
  let deleting = $state<Project | undefined>();
  let confirmOpen = $state(false);

  type Action = { status: string; error?: { code: string; message: string } | null };

  async function waitAction(id: number): Promise<void> {
    for (;;) {
      const { action: a } = await mgmt<{ action: Action }>("GET", `/actions/${id}`);
      if (a.status === "success") return;
      if (a.status === "error") throw new ApiError(a.error?.code ?? "action_failed", a.error?.message ?? "the action failed");
      await new Promise((r) => setTimeout(r, 1000));
    }
  }

  function askDelete(p: Project) {
    deleting = p;
    confirmOpen = true;
  }

  function remove() {
    const { name, display_name } = deleting!;
    // Not awaited: the dialog closes, the toast shows the progress.
    action(actionTitle("Project", display_name, "Delete"), async () => {
      const { action_id } = await mgmt<{ action_id: number }>("DELETE", `/projects/${encodeURIComponent(name)}`);
      await waitAction(action_id);
    }).finally(load);
  }
</script>

<svelte:window onclick={(e) => sortOpen && !sortRoot?.contains(e.target as Node) && (sortOpen = false)} />

<div class="title-row">
  <h1>Projects</h1>
  <HelpButton view="projects" />
</div>

{#if $running}
  <div class="toolbar">
    <input type="search" aria-label="Search" placeholder="Search by display name or name" bind:value={query} />
    <div class="sort" bind:this={sortRoot}>
      <Button aria-haspopup="menu" aria-expanded={sortOpen} onclick={toggleSort}>
        Sort: {sortLabels[$projectSort.key]}<ChevronDown size={16} aria-hidden="true" />
      </Button>
      {#if sortOpen}
        <ul role="menu" aria-label="Sort" bind:this={sortMenu} onkeydown={sortKeydown}>
          {#each sortKeys as key (key)}
            <li role="none">
              <button role="menuitem" class:current={$projectSort.key === key} onclick={() => pickSort(key)}>
                {sortLabels[key]}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    <Button variant="icon" label={$projectSort.dir === "asc" ? "Ascending" : "Descending"} onclick={reverse}>
      {#if $projectSort.dir === "asc"}<ArrowUp size={16} aria-hidden="true" />{:else}<ArrowDown size={16} aria-hidden="true" />{/if}
    </Button>
    <div class="switch" role="group" aria-label="Layout">
      <Button variant="icon" label="Grid" aria-pressed={$projectLayout === "grid"} onclick={() => projectLayout.set("grid")}>
        <LayoutGrid size={16} aria-hidden="true" />
      </Button>
      <Button variant="icon" label="List" aria-pressed={$projectLayout === "list"} onclick={() => projectLayout.set("list")}>
        <ListIcon size={16} aria-hidden="true" />
      </Button>
    </div>
  </div>

  {#if rows && rows.length === 0}
    <p class="empty">There are no projects yet.</p>
  {:else if shown && shown.length === 0}
    <p class="empty">Nothing matches the search.</p>
  {/if}

  <div class={["cards", $projectLayout]} aria-busy={!shown}>
    {#if !shown}
      {#each [1, 2, 3] as i (i)}
        <div class="skeleton"><span class="bar"></span><span class="bar short"></span></div>
      {/each}
    {:else}
      {#each shown as p (p.name)}
        <ProjectCard project={p} layout={$projectLayout} ondelete={askDelete} />
      {/each}
      <button class="tile" onclick={openCreate}><Plus size={16} aria-hidden="true" />New project</button>
    {/if}
  </div>
{:else}
  <CloudStopped />
{/if}

<dialog bind:this={createDialog} aria-labelledby="create-project-title">
  <form onsubmit={create}>
    <h2 id="create-project-title">Create project</h2>
    <Field label="Display name" error={nameError} hint={`Name: ${previewName || "–"}`}>
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={newName} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => createDialog.close()} disabled={creating}>Cancel</Button>
      <Button type="submit" variant="primary" loading={creating}>Create</Button>
    </div>
  </form>
</dialog>

<ConfirmDialog bind:open={confirmOpen} title="Delete project" confirmLabel="Delete" typedName={deleting?.display_name ?? ""} onconfirm={remove}>
  <p>Deleting <strong>{deleting?.display_name}</strong> deletes all its resources and revokes its tokens.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  .toolbar {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  input[type="search"] {
    width: 280px;
    margin-right: auto;
  }
  .sort {
    position: relative;
  }
  .sort ul {
    position: absolute;
    right: 0;
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
    white-space: nowrap;
    cursor: pointer;
  }
  [role="menuitem"].current {
    font-weight: 600;
  }
  [role="menuitem"]:hover,
  [role="menuitem"]:focus-visible {
    background: var(--bg);
  }
  [role="menuitem"]:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .switch {
    display: flex;
    gap: var(--s1);
  }
  .switch :global([aria-pressed="true"]) {
    background: var(--border);
  }
  .cards {
    display: grid;
    gap: var(--s4);
  }
  .cards.grid {
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  }
  .cards.list {
    grid-template-columns: 1fr;
    gap: var(--s2);
  }
  .empty {
    margin-bottom: var(--s4);
    color: var(--muted);
  }
  .tile {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--s2);
    box-sizing: border-box;
    min-height: 128px;
    padding: var(--s4);
    border: 1px dashed var(--border);
    border-radius: var(--radius);
    background: none;
    color: var(--accent);
    font: inherit;
    cursor: pointer;
  }
  .list .tile {
    min-height: 0;
    padding: var(--s3) var(--s4);
  }
  .tile:hover {
    border-color: var(--accent);
  }
  .tile:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .skeleton {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    box-sizing: border-box;
    min-height: 128px;
    padding: var(--s4);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .list .skeleton {
    min-height: 0;
  }
  .bar {
    display: block;
    height: 12px;
    border-radius: var(--radius);
    background: var(--border);
  }
  .bar.short {
    width: 40%;
  }
  dialog {
    width: 440px;
    padding: var(--s6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.4);
  }
  h2 {
    margin-bottom: var(--s4);
  }
  dialog input {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
</style>
