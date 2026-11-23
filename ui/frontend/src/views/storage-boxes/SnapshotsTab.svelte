<script module lang="ts">
  import type { Snap } from "../../lib/resources/storageBoxSnapshots";
  import { weekdays } from "./SnapshotPlanDialog.svelte";

  /** BoxSnapshot is a storage box snapshot as the Hetzner API returns it (the fields the tab uses). */
  export type BoxSnapshot = Snap & {
    description: string;
    is_automatic: boolean;
    stats: { size: number };
    labels: Record<string, string>;
  };

  const two = (n: number | null | undefined) => String(n ?? 0).padStart(2, "0");

  /** planText describes a snapshot plan in UTC, "No plan" without one. */
  export function planText(plan: Record<string, number | null> | null): string {
    if (!plan) return "No plan";
    const dow = plan.day_of_week;
    const dom = plan.day_of_month;
    const days = [dow ? `on ${weekdays[dow - 1]}` : "", dom ? `on day ${dom} of the month` : ""].filter(Boolean);
    return `${days.length ? days.join(" and ") : "Every day"} at ${two(plan.hour)}:${two(plan.minute)} UTC, keeps ${plan.max_snapshots} snapshots`;
  }
</script>

<script lang="ts">
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { hetznerListAll, runHetznerOn } from "../../lib/api";
  import { bytesText, createdText } from "../../lib/format";
  import { request, type Input } from "../../lib/resourceActions";
  import type { StorageBox } from "../../lib/resources/storageBoxes";
  import { newerThan, tabs } from "../../lib/resources/storageBoxSnapshots";
  import { showError } from "../../lib/toast";
  import SnapshotPlanDialog from "./SnapshotPlanDialog.svelte";

  // The Snapshots tab of a storage box: the snapshot plan with "Set plan"
  // and "Disable plan", and the snapshots with create, edit description,
  // roll back and delete. The list refreshes every 5 s; refresh reloads
  // the box (its plan and usage) after an action.
  let { project, box, refresh }: { project: string; box: StorageBox; refresh: () => void } = $props();

  let rows = $state<BoxSnapshot[] | undefined>();
  let failing = false;

  const columns: Column<BoxSnapshot>[] = [
    { key: "name", label: "Name", value: (s) => s.name, sortable: true },
    { key: "description", label: "Description", value: (s) => s.description, sortable: true },
    { key: "size", label: "Size", value: (s) => bytesText(s.stats.size) },
    { key: "automatic", label: "Automatic", value: (s) => (s.is_automatic ? "Yes" : "No"), sortable: true },
    { key: "created", label: "Created", value: (s) => createdText(s.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await hetznerListAll<BoxSnapshot>(project, `/storage_boxes/${box.id}/snapshots`, "snapshots");
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

  // run sends one tab action under the toast "Storage Box <name>: <action>",
  // then reloads the list and the box.
  async function run(id: string, input: Input = {}) {
    const a = tabs[id];
    const req = request(a, box.id, input);
    if (!req) return;
    await runHetznerOn(project, box.name, a.label, req);
    load();
    refresh();
  }

  // Create and Edit description share one dialog.
  let editing = $state<BoxSnapshot | undefined>();
  let descOpen = $state(false);
  let description = $state("");

  function openDescription(s?: BoxSnapshot) {
    editing = s;
    description = s?.description ?? "";
    descOpen = true;
  }

  function saveDescription(e: SubmitEvent) {
    e.preventDefault();
    descOpen = false;
    const d = description.trim();
    if (editing) run("update", { snapshot: editing.id, description: d });
    else run("create", d ? { description: d } : {});
  }

  // Roll back and Delete confirm first.
  let pending = $state<{ id: "rollback" | "delete"; snap: BoxSnapshot } | undefined>();
  let confirmOpen = $state(false);
  const deleted = $derived(pending?.id === "rollback" && rows ? newerThan(rows, pending.snap) : []);

  function ask(id: "rollback" | "delete", snap: BoxSnapshot) {
    pending = { id, snap };
    confirmOpen = true;
  }

  function confirmed() {
    if (!pending) return;
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(pending.id, { snapshot: pending.snap.id });
  }

  let planOpen = $state(false);
  let disableOpen = $state(false);
</script>

<section class="plan">
  <div>
    <h2>Snapshot plan</h2>
    <p>{planText(box.snapshot_plan)}</p>
  </div>
  <div class="buttons">
    <Button onclick={() => (planOpen = true)}>Set plan</Button>
    {#if box.snapshot_plan}<Button onclick={() => (disableOpen = true)}>Disable plan</Button>{/if}
  </div>
</section>

<Table
  title="Snapshots"
  {rows}
  {columns}
  key={(s) => s.id}
  name={(s) => s.name}
  labels={(s) => s.labels}
  emptyText="This storage box has no snapshots yet."
  createLabel="Create snapshot"
  oncreate={() => openDescription()}
  actions={(s) => [
    { label: "Edit", onclick: () => openDescription(s) },
    { label: "Roll back", onclick: () => ask("rollback", s) },
    { label: "Delete", onclick: () => ask("delete", s) },
  ]}
/>

<Dialog bind:open={descOpen} title={editing ? `Edit snapshot ${editing.name}` : "Create snapshot"}>
  <form onsubmit={saveDescription}>
    <Field label="Description" hint={editing ? undefined : "Optional."}>
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={description} />
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (descOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary">{editing ? "Save" : "Create snapshot"}</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={confirmOpen}
  title={pending?.id === "rollback" ? "Roll back storage box" : "Delete snapshot"}
  confirmLabel={pending?.id === "rollback" ? "Roll back" : "Delete"}
  onconfirm={confirmed}
>
  {#if pending?.id === "rollback"}
    <p>The storage box <strong>{box.name}</strong> is set back to the snapshot <strong>{pending.snap.name}</strong>. All data written since is lost.</p>
    {#if deleted.length > 0}
      <p>These newer snapshots are deleted:</p>
      <ul>
        {#each deleted as s (s.id)}<li>{s.name}</li>{/each}
      </ul>
    {:else}
      <p>No newer snapshot is deleted.</p>
    {/if}
  {:else if pending}
    <p>The snapshot <strong>{pending.snap.name}</strong> is deleted for good.</p>
  {/if}
</ConfirmDialog>

<SnapshotPlanDialog bind:open={planOpen} plan={box.snapshot_plan} onsave={(body) => run("enable_plan", body)} />

<ConfirmDialog
  bind:open={disableOpen}
  title="Disable snapshot plan"
  confirmLabel="Disable plan"
  onconfirm={() => {
    run("disable_plan");
  }}
>
  <p>The storage box <strong>{box.name}</strong> takes no more automatic snapshots; the ones it has stay.</p>
</ConfirmDialog>

<style>
  .plan {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    padding: var(--s4);
    margin-bottom: var(--s6);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }
  .buttons {
    display: flex;
    gap: var(--s2);
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
