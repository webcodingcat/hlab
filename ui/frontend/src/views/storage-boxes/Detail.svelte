<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import RenameInput from "../../lib/RenameInput.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { hetznerApi } from "../../lib/api";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { menu, type StorageBox } from "../../lib/resources/storageBoxes";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import OverviewTab from "./OverviewTab.svelte";
  import SettingsTab from "./SettingsTab.svelte";
  import SnapshotsTab from "./SnapshotsTab.svelte";
  import SubaccountsTab from "./SubaccountsTab.svelte";

  // The detail page of one storage box: title with status pill, actions
  // menu (Rename, Delete with the name typed, disabled under delete
  // protection) and the tabs Overview, Subaccounts, Snapshots, Settings and
  // Labels. The box refreshes every 5 s while it is shown.
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  const path = $derived(`/storage_boxes/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "subaccounts", label: "Subaccounts" },
      { id: "snapshots", label: "Snapshots" },
      { id: "settings", label: "Settings" },
      { id: "labels", label: "Labels" },
    ].map((t) => ({
      ...t,
      href: href({ area: "project", project, view: "storage-boxes", page: { kind: "detail", id, tab: t.id } }),
    })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let box = $state<StorageBox | undefined>();
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      box = (await hetznerApi<{ storage_box: StorageBox }>(project, "GET", path)).storage_box;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load the storage box", err);
      failing = true;
    }
  }

  $effect(() => {
    void path;
    untrack(() => {
      box = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const tone = (status: string) => (status === "active" ? "success" : status === "initializing" ? "warning" : "muted");

  const menuActions = $derived(
    menu.map((a): ResourceAction =>
      a.id === "delete" && box?.protection.delete
        ? { ...a, disabledHint: "Delete protection is on; turn it off in Settings first." }
        : a,
    ),
  );

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Storage Box" help="storage-boxes" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !box}
  <p class="muted" aria-busy="true">Loading the storage box…</p>
{:else}
  {@const b = box}
  <DetailPage title={b.name} status={{ tone: tone(b.status), text: b.status }} help="storage-boxes" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={b.id} name={b.name} actions={menuActions} ondone={done} hetzner>
        {#snippet input(_a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          <RenameInput current={b.name} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={b.labels} onsaved={load} hetzner />
    {:else if current === "settings"}
      <SettingsTab {project} box={b} refresh={load} />
    {:else if current === "subaccounts"}
      <SubaccountsTab {project} box={b} refresh={load} />
    {:else if current === "snapshots"}
      <SnapshotsTab {project} box={b} refresh={load} />
    {:else if current === "overview"}
      <OverviewTab {project} box={b} />
    {/if}
  </DetailPage>
{/if}

<style>
  .muted {
    color: var(--muted);
  }
</style>
