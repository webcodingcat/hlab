<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";
  import { runTab } from "./run";

  // The Firewalls tab: the firewalls applied to the server (apply one,
  // remove one). A firewall applied through a label selector is removed in
  // the firewall, not here.
  type Firewall = { id: number; name: string; applied_to: { type: string; server?: { id: number } }[] };
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let firewalls = $state<Firewall[] | undefined>();
  let applyOpen = $state(false);
  let chosen = $state("");
  let removeOpen = $state(false);
  let removing = $state<Firewall | undefined>();

  const rows = $derived(
    server.public_net.firewalls.map((f) => {
      const fw = firewalls?.find((x) => x.id === f.id);
      const direct = fw?.applied_to.some((r) => r.type === "server" && r.server?.id === server.id) ?? false;
      return { id: f.id, name: fw?.name ?? `firewall ${f.id}`, status: f.status, direct, fw };
    }),
  );
  const free = $derived((firewalls ?? []).filter((f) => !server.public_net.firewalls.some((x) => x.id === f.id)));

  async function load() {
    try {
      firewalls = await listAll<Firewall>(project, "/firewalls", "firewalls");
    } catch (err) {
      showError("Load firewalls", err);
    }
  }

  $effect(() => {
    load();
  });

  async function run(id: string, firewall: number) {
    if (await runTab(project, server, id, { firewall })) {
      onchanged();
      load();
    }
  }

  function apply(e: SubmitEvent) {
    e.preventDefault();
    applyOpen = false;
    run("apply_firewall", Number(chosen));
  }
</script>

<section>
  <header>
    <h2>Firewalls</h2>
    <Button
      variant="primary"
      onclick={() => {
        chosen = "";
        applyOpen = true;
        load();
      }}>Apply firewall</Button
    >
  </header>
  {#if rows.length === 0}
    <p class="muted">No firewall is applied to this server.</p>
  {:else}
    <table>
      <thead><tr><th>Name</th><th>Applied</th><th>Status</th><th><span class="hidden">Actions</span></th></tr></thead>
      <tbody>
        {#each rows as r (r.id)}
          <tr>
            <td>{r.name}</td>
            <td>{r.direct ? "To this server" : "Through a label selector"}</td>
            <td>{r.status}</td>
            <td class="row-actions">
              {#if r.direct}
                <Button
                  onclick={() => {
                    removing = r.fw;
                    removeOpen = true;
                  }}>Remove</Button
                >
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={applyOpen} title={`Apply firewall to ${server.name}`}>
  <form onsubmit={apply}>
    <Field label="Firewall">
      {#snippet children(c)}
        <select id={c.id} bind:value={chosen}>
          <option value="" disabled>Choose a firewall</option>
          {#each free as f (f.id)}<option value={`${f.id}`}>{f.name}</option>{/each}
        </select>
      {/snippet}
    </Field>
    {#if firewalls && free.length === 0}<p class="muted">No other firewall exists in this project.</p>{/if}
    <div class="actions">
      <Button onclick={() => (applyOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={chosen === ""}>Apply</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={removeOpen}
  title="Remove firewall"
  confirmLabel="Remove"
  onconfirm={() => {
    if (removing) run("remove_firewall", removing.id);
  }}
>
  <p>The firewall {removing?.name ?? ""} no longer filters the traffic of <strong>{server.name}</strong>.</p>
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
  select {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
