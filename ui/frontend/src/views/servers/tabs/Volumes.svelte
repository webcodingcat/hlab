<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";
  import { runTab } from "./run";

  // The Volumes tab: the volumes attached to the server (detach) and attach
  // of a free volume in the server's location, with automount. Refreshed
  // every 5 s.
  type Volume = { id: number; name: string; size: number; server: number | null; location: { name: string }; linux_device: string };
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let volumes = $state<Volume[] | undefined>();
  let attachOpen = $state(false);
  let chosen = $state("");
  let automount = $state(false);
  let detachOpen = $state(false);
  let detaching = $state<Volume | undefined>();
  let failing = false;

  const attached = $derived((volumes ?? []).filter((v) => v.server === server.id));
  const free = $derived((volumes ?? []).filter((v) => v.server === null && v.location.name === server.location.name));

  async function load() {
    try {
      volumes = await listAll<Volume>(project, "/volumes", "volumes");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load volumes", err);
      failing = true;
    }
  }

  $effect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  async function run(id: string, input: Record<string, unknown>) {
    if (await runTab(project, server, id, input)) {
      onchanged();
      load();
    }
  }

  function attach(e: SubmitEvent) {
    e.preventDefault();
    attachOpen = false;
    run("attach_volume", { volume: Number(chosen), automount });
  }
</script>

<section>
  <header>
    <h2>Volumes</h2>
    <Button
      variant="primary"
      onclick={() => {
        chosen = "";
        automount = false;
        attachOpen = true;
      }}>Attach volume</Button
    >
  </header>
  {#if !volumes}
    <p class="muted" aria-busy="true">Loading the volumes…</p>
  {:else if attached.length === 0}
    <p class="muted">No volume is attached to this server.</p>
  {:else}
    <table>
      <thead><tr><th>Name</th><th>Size</th><th>Device</th><th><span class="hidden">Actions</span></th></tr></thead>
      <tbody>
        {#each attached as v (v.id)}
          <tr>
            <td>{v.name}</td>
            <td>{v.size} GB</td>
            <td class="mono">{v.linux_device}</td>
            <td class="row-actions">
              <Button
                onclick={() => {
                  detaching = v;
                  detachOpen = true;
                }}>Detach</Button
              >
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={attachOpen} title={`Attach volume to ${server.name}`}>
  <form onsubmit={attach}>
    <Field label="Volume" hint={`Free volumes in ${server.location.name}`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={chosen}>
          <option value="" disabled>Choose a volume</option>
          {#each free as v (v.id)}<option value={`${v.id}`}>{v.name} ({v.size} GB)</option>{/each}
        </select>
      {/snippet}
    </Field>
    {#if free.length === 0}<p class="muted">No free volume in this location.</p>{/if}
    <label class="check">
      <input type="checkbox" role="switch" aria-checked={automount} bind:checked={automount} />Mount automatically
    </label>
    <div class="actions">
      <Button onclick={() => (attachOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={chosen === ""}>Attach</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={detachOpen}
  title="Detach volume"
  confirmLabel="Detach"
  onconfirm={() => {
    if (detaching) run("detach_volume", { volume: detaching.id });
  }}
>
  <p>The volume {detaching?.name ?? ""} is detached from <strong>{server.name}</strong>. Unmount it in the server first.</p>
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
  .row-actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
  .mono {
    font-family: var(--mono);
  }
  .muted {
    color: var(--muted);
  }
  .hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
