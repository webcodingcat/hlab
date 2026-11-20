<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import RenameInput from "../../lib/RenameInput.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { api, listAll } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/placementGroups";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import type { PlacementGroup } from "./Section.svelte";

  // The detail page of one placement group: Overview (type and its servers,
  // each linking to its page) and Labels; the actions menu renames and
  // deletes it (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Named = { id: number; name: string };

  const path = $derived(`/placement_groups/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "placement-groups", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let group = $state<PlacementGroup | undefined>();
  let servers = $state<Named[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [g, s] = await Promise.all([
        api<{ placement_group: PlacementGroup }>(project, "GET", path),
        listAll<Named>(project, "/servers", "servers"),
      ]);
      group = g.placement_group;
      servers = s;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load placement group", err);
      failing = true;
    }
  }

  // The open placement group refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      group = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const serverName = (sid: number) => servers.find((s) => s.id === sid)?.name ?? `${sid}`;
  const serverHref = (sid: number) =>
    href({ area: "project", project, view: "servers", page: { kind: "detail", id: sid, tab: "overview" } });

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Placement group" help="placement-groups" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !group}
  <p class="muted" aria-busy="true">Loading the placement group…</p>
{:else}
  {@const g = group}
  <DetailPage title={g.name} help="placement-groups" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={g.id} name={g.name} actions={menu} ondone={done}>
        {#snippet input(_a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          <RenameInput current={g.name} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={g.labels} onsaved={load} />
    {:else}
      <dl>
        <dt>Type</dt>
        <dd>{g.type}</dd>
        <dt>Servers</dt>
        <dd>
          {#each g.servers as sid (sid)}
            <div><a href={serverHref(sid)}>{serverName(sid)}</a></div>
          {:else}
            <span class="muted">No servers</span>
          {/each}
        </dd>
        <dt>Created</dt>
        <dd>{createdText(g.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
{/if}

<style>
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--s2) var(--s4);
    max-width: 640px;
    margin: 0;
  }
  dt {
    color: var(--muted);
    font-weight: 600;
  }
  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .muted {
    color: var(--muted);
  }
</style>
