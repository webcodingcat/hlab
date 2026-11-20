<script module lang="ts">
  /** Iso as the Hetzner API returns it (the fields the views use). */
  export type Iso = {
    id: number;
    name: string;
    description: string;
    type: string;
    architecture: string | null;
    deprecation: { announced: string; unavailable_after: string } | null;
  };

  /** deprecatedText says whether an ISO is deprecated. */
  export const deprecatedText = (i: Iso) => (i.deprecation ? "Yes" : "No");
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll } from "../../lib/api";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import IsoDetail from "./IsoDetail.svelte";

  // The "ISOs" section of a project console: a read-only list of all ISOs
  // on one page and the detail page of one ISO.
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "isos", page: { kind: "list" } }));

  let rows = $state<Iso[] | undefined>();

  const columns: Column<Iso>[] = [
    { key: "name", label: "Name", value: (i) => i.name, sortable: true },
    { key: "description", label: "Description", value: (i) => i.description, sortable: true },
    { key: "architecture", label: "Architecture", value: (i) => i.architecture ?? "", sortable: true },
    { key: "deprecated", label: "Deprecated", value: deprecatedText, sortable: true },
  ];

  // The catalog does not change while the cloud runs: load it once per visit.
  $effect(() => {
    if (!$running || page.kind !== "list") return;
    rows = undefined;
    listAll<Iso>(project, "/isos", "isos")
      .then((all) => (rows = all))
      .catch((err) => showError("Load ISOs", err));
  });

  function open(i: Iso) {
    location.hash = href({ area: "project", project, view: "isos", page: { kind: "detail", id: i.id, tab: "overview" } });
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>ISOs</h1>
    <HelpButton view="isos" />
  </div>
  <CloudStopped />
{:else if page.kind === "detail"}
  <IsoDetail {project} id={page.id} {list} />
{:else}
  <div class="title-row">
    <h1>ISOs</h1>
    <HelpButton view="isos" />
  </div>
  <Table
    title="ISOs"
    {rows}
    {columns}
    key={(i) => i.id}
    name={(i) => i.name}
    emptyText="No ISOs are available."
    onrowclick={open}
  />
{/if}

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
</style>
