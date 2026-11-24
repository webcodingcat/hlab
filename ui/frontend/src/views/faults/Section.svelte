<script lang="ts">
  import Button from "../../lib/Button.svelte";
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import FaultsTable from "../../lib/FaultsTable.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import { hetznerListAll, listAll } from "../../lib/api";
  import { mgmt } from "../../lib/bridge";
  import { kindLabel, targetText, type Fault } from "../../lib/faults";
  import { actionTitle } from "../../lib/format";
  import { action, showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";

  // The "Faults" section of a project console: the project's active faults
  // from the management API with the names of its servers, volumes and
  // storage boxes for the target column.
  let { project, page }: { project: string; page: Page } = $props();

  const base = $derived(`/projects/${encodeURIComponent(project)}/faults`);

  let rows = $state<Fault[] | undefined>();
  let names = $state(new Map<string, string>());
  let failing = false;

  type Named = { id: number; name: string };

  async function load() {
    try {
      const [f, servers, volumes, boxes] = await Promise.all([
        mgmt<{ faults: Fault[] | null }>("GET", base),
        listAll<Named>(project, "/servers", "servers"),
        listAll<Named>(project, "/volumes", "volumes"),
        hetznerListAll<Named>(project, "/storage_boxes", "storage_boxes"),
      ]);
      names = new Map([
        ...servers.map((s) => [`server:${s.id}`, s.name] as const),
        ...volumes.map((v) => [`volume:${v.id}`, v.name] as const),
        ...boxes.map((b) => [`storage_box:${b.id}`, b.name] as const),
      ]);
      rows = f.faults ?? [];
      failing = false;
    } catch (err) {
      if (!failing) showError("Load faults", err);
      failing = true;
    }
  }

  // The list refreshes every 5 s while it is shown.
  $effect(() => {
    if (!$running || page.kind !== "list") return;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function openCreate() {
    location.hash = href({ area: "project", project, view: "faults", page: { kind: "new" } });
  }

  async function end(f: Fault) {
    await action(actionTitle(kindLabel(f.kind), targetText(f.target, names), "End"), () =>
      mgmt("DELETE", `${base}/${f.id}`),
    );
    await load();
  }

  let confirmOpen = $state(false);

  function endAll() {
    // Not awaited: the dialog closes at once, the toast follows the request.
    action(actionTitle("Faults", "", "End all"), () => mgmt("DELETE", base)).then(load);
  }
</script>

<div class="title-row">
  <h1>Faults</h1>
  <HelpButton view="faults" />
</div>

{#if !$running}
  <CloudStopped />
{:else if page.kind === "list"}
  {#if !rows}
    <p class="muted">Loading faults…</p>
  {:else if rows.length === 0}
    <div class="empty">
      <p>No faults are active in this project.</p>
      <Button variant="primary" onclick={openCreate}>Create fault</Button>
    </div>
  {:else}
    <div class="toolbar">
      <Button variant="danger" onclick={() => (confirmOpen = true)}>End all</Button>
      <Button variant="primary" onclick={openCreate}>Create fault</Button>
    </div>
    <FaultsTable faults={rows} {names} onEnd={end} />
  {/if}
{/if}

<ConfirmDialog bind:open={confirmOpen} title="End all faults" confirmLabel="End all" onconfirm={endAll}>
  <p>Every active fault of this project ends now.</p>
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
    justify-content: flex-end;
    gap: var(--s2);
    margin-bottom: var(--s3);
  }
  .empty {
    padding: var(--s8);
    text-align: center;
    background: var(--surface);
    border: 1px solid var(--border);
  }
</style>
