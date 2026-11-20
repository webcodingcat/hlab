<script lang="ts">
  import Button from "../lib/Button.svelte";
  import CloudStopped from "../lib/CloudStopped.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import Table, { type Column } from "../lib/Table.svelte";
  import { ApiError, mgmt } from "../lib/bridge";
  import { action, showError } from "../lib/toast";
  import { href, lastProject, running, type Project } from "../state";

  // The project list, full width; a row click opens the project's console.
  let rows = $state<Project[] | undefined>();
  let failing = false;

  const columns: Column<Project>[] = [
    { key: "name", label: "Name", value: (p) => p.name, sortable: true },
    { key: "resources", label: "Resources", value: (p) => p.resources, sortable: true },
    { key: "created", label: "Created", value: (p) => p.created.slice(0, 16).replace("T", " "), sortable: true },
  ];

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

  function openProject(p: Project) {
    lastProject.set(p.name);
    location.hash = href({ area: "project", project: p.name, view: "tokens" });
  }

  // Create dialog.
  let createDialog: HTMLDialogElement;
  let newName = $state("");
  let nameError = $state<string | undefined>();
  let creating = $state(false);

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
      await mgmt("POST", "/projects", { name: newName.trim() });
      createDialog.close();
      await load();
    } catch (err) {
      if (err instanceof ApiError && (err.code === "invalid_input" || err.code === "conflict")) {
        nameError = err.fields?.name?.join(" ") || err.message;
      } else {
        showError("Create project", err);
      }
    } finally {
      creating = false;
    }
  }

  // Delete: the name must be typed; the deletion runs as an action.
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

  function remove() {
    const name = deleting!.name;
    // Not awaited: the dialog closes, the toast shows the progress.
    action(`Delete project ${name}`, async () => {
      const { action_id } = await mgmt<{ action_id: number }>("DELETE", `/projects/${encodeURIComponent(name)}`);
      await waitAction(action_id);
    }).finally(load);
  }
</script>

<div class="title-row">
  <h1>Projects</h1>
  <HelpButton view="projects" />
</div>

{#if $running}
  <Table
    title="Projects"
    {rows}
    {columns}
    key={(p) => p.name}
    name={(p) => p.name}
    emptyText="There are no projects yet."
    createLabel="Create project"
    oncreate={openCreate}
    onrowclick={openProject}
    actions={(p) => [{ label: "Delete", onclick: () => ((deleting = p), (confirmOpen = true)) }]}
  />
{:else}
  <CloudStopped />
{/if}

<dialog bind:this={createDialog} aria-labelledby="create-project-title">
  <form onsubmit={create}>
    <h2 id="create-project-title">Create project</h2>
    <Field label="Name" error={nameError} hint="1 to 63 characters: a-z, 0-9 and -">
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

<ConfirmDialog bind:open={confirmOpen} title="Delete project" confirmLabel="Delete" typedName={deleting?.name ?? ""} onconfirm={remove}>
  <p>Deleting <strong>{deleting?.name}</strong> deletes all its resources and revokes its tokens.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
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
  input {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
</style>
