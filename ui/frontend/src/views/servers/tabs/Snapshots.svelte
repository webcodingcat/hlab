<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";
  import ImageTable, { type ServerImage } from "./ImageTable.svelte";
  import { runTab } from "./run";

  // The Snapshots tab: create a snapshot with an optional description and
  // the snapshots taken of this server with Restore and Delete. Refreshed
  // every 5 s.
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let images = $state<ServerImage[] | undefined>();
  let createOpen = $state(false);
  let description = $state("");
  let failing = false;

  async function load() {
    try {
      const all = await listAll<ServerImage>(project, "/images?type=snapshot", "images");
      images = all.filter((i) => i.created_from?.id === server.id).sort((a, b) => b.created.localeCompare(a.created));
      failing = false;
    } catch (err) {
      if (!failing) showError("Load snapshots", err);
      failing = true;
    }
  }

  $effect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function changed() {
    onchanged();
    load();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    createOpen = false;
    const d = description.trim();
    if (await runTab(project, server, "create_snapshot", d ? { description: d } : {})) changed();
  }
</script>

<section>
  <header>
    <h2>Snapshots</h2>
    <Button
      variant="primary"
      onclick={() => {
        description = "";
        createOpen = true;
      }}>Create snapshot</Button
    >
  </header>
  <ImageTable {project} {server} kind="snapshot" {images} onchanged={changed} />
</section>

<Dialog bind:open={createOpen} title={`Create snapshot of ${server.name}`}>
  <form onsubmit={create}>
    <Field label="Description" hint="Optional; without one the snapshot is named after the server and the time.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={description} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (createOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary">Create snapshot</Button>
    </div>
  </form>
</Dialog>

<style>
  section {
    max-width: 960px;
    margin-bottom: var(--s6);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--s3);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  input {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
