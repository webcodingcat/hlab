<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";
  import ImageTable, { type ServerImage } from "./ImageTable.svelte";
  import { runTab } from "./run";

  // The Backups tab: enable or disable daily backups, create a backup now
  // and the server's backups with Restore and Delete. Refreshed every 5 s.
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let images = $state<ServerImage[] | undefined>();
  let disableOpen = $state(false);
  let failing = false;

  const enabled = $derived(server.backup_window !== null);

  async function load() {
    try {
      const all = await listAll<ServerImage>(project, `/images?type=backup&bound_to=${server.id}`, "images");
      images = all.sort((a, b) => b.created.localeCompare(a.created));
      failing = false;
    } catch (err) {
      if (!failing) showError("Load backups", err);
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

  async function run(id: string) {
    if (await runTab(project, server, id)) changed();
  }
</script>

<section>
  <header>
    <h2>Backups</h2>
    <div class="buttons">
      {#if enabled}
        <Button onclick={() => (disableOpen = true)}>Disable backups</Button>
        <Button variant="primary" onclick={() => run("create_backup")}>Create backup</Button>
      {:else}
        <Button variant="primary" onclick={() => run("enable_backup")}>Enable backups</Button>
      {/if}
    </div>
  </header>
  <p class="muted">
    {#if enabled}
      Backups are enabled: one backup per day while the cloud runs, the 7 newest are kept.
    {:else}
      Backups are disabled.
    {/if}
  </p>
  <ImageTable {project} {server} kind="backup" {images} onchanged={changed} />
</section>

<ConfirmDialog bind:open={disableOpen} title="Disable backups" confirmLabel="Disable backups" onconfirm={() => run("disable_backup")}>
  <p>Backups of <strong>{server.name}</strong> stop and all backups of this server are deleted for good.</p>
</ConfirmDialog>

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
  .buttons {
    display: flex;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
</style>
