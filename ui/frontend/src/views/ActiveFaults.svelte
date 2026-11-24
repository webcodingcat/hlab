<script lang="ts">
  import Button from "../lib/Button.svelte";
  import CloudStopped from "../lib/CloudStopped.svelte";
  import ConfirmDialog from "../lib/ConfirmDialog.svelte";
  import FaultsTable from "../lib/FaultsTable.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import { faultTargetNames } from "../lib/api";
  import { mgmt } from "../lib/bridge";
  import { kindLabel, targetText, type Fault } from "../lib/faults";
  import { actionTitle } from "../lib/format";
  import { action, showError } from "../lib/toast";
  import { running, type Project } from "../state";

  // "Active faults": the active faults of every project with a Project
  // column, the names of their targets from each project's servers, volumes
  // and storage boxes (IDs are global per kind, so one map serves all); a
  // name list that fails leaves those names out, the faults still show.
  let rows = $state<Fault[] | undefined>();
  let projects = $state<{ name: string; display: string }[]>([]);
  let names = $state(new Map<string, string>());
  let failing = false;

  async function load() {
    try {
      const [f, p] = await Promise.all([
        mgmt<{ faults: Fault[] | null }>("GET", "/faults"),
        mgmt<{ projects: Project[] | null }>("GET", "/projects"),
      ]);
      const faults = f.faults ?? [];
      const withFaults = [...new Set(faults.map((x) => x.project))];
      const lists = await Promise.all(withFaults.map(faultTargetNames));
      projects = (p.projects ?? []).map((x) => ({ name: x.name, display: x.display_name }));
      names = new Map(lists.flat());
      rows = faults;
      failing = false;
    } catch (err) {
      if (!failing) showError("Load faults", err);
      failing = true;
    }
  }

  // The list refreshes every 5 s while it is shown.
  $effect(() => {
    if (!$running) return;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  async function end(f: Fault) {
    await action(actionTitle(kindLabel(f.kind), targetText(f.target, names), "End"), () =>
      mgmt("DELETE", `/projects/${encodeURIComponent(f.project)}/faults/${f.id}`),
    );
    await load();
  }

  let confirmOpen = $state(false);

  function endAll() {
    // Not awaited: the dialog closes at once, the toast follows the request.
    action(actionTitle("Faults", "", "End all"), () => mgmt("DELETE", "/faults")).then(load);
  }
</script>

<div class="title-row">
  <h1>Active faults</h1>
  <HelpButton view="active-faults" />
</div>

{#if !$running}
  <CloudStopped />
{:else if !rows}
  <p class="muted">Loading faults…</p>
{:else if rows.length === 0}
  <div class="empty"><p>No faults are active.</p></div>
{:else}
  <div class="toolbar">
    <Button variant="danger" onclick={() => (confirmOpen = true)}>End all</Button>
  </div>
  <FaultsTable faults={rows} {names} withProject={projects} onEnd={end} />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="End all faults" confirmLabel="End all" onconfirm={endAll}>
  <p>Every active fault of every project ends now.</p>
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
