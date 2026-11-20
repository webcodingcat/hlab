<script lang="ts">
  import Dialog from "../../../lib/Dialog.svelte";
  import ResourceActionsMenu from "../../../lib/ResourceActionsMenu.svelte";
  import Table, { type Column } from "../../../lib/Table.svelte";
  import { listAll, runOn } from "../../../lib/api";
  import { labelText } from "../../../lib/format";
  import { request, type Input, type ResourceAction } from "../../../lib/resourceActions";
  import { tabs } from "../../../lib/resources/zones";
  import { showError } from "../../../lib/toast";
  import type { Zone } from "../Section.svelte";
  import RRSetInput, { type RRSet } from "./RRSetInput.svelte";

  // The Records tab of a primary zone: its RRSets with name, type, TTL,
  // values and labels. "Add record set" creates one; each row's menu
  // changes the TTL, adds and removes records, edits labels and protection
  // and deletes the RRSet. The SOA is HLab's: its menu has only Change TTL.
  let { project, zone, onchanged }: { project: string; zone: Zone; onchanged: () => void } = $props();

  // zoneId changes only with the zone, not with each refresh of its object.
  const zoneId = $derived(zone.id);
  let rows = $state<RRSet[] | undefined>();
  let failing = false;

  const columns: Column<RRSet>[] = [
    { key: "name", label: "Name", value: (r) => r.name, sortable: true },
    { key: "type", label: "Type", value: (r) => r.type, sortable: true },
    { key: "ttl", label: "TTL", value: (r) => (r.ttl === null ? `Default (${zone.ttl})` : r.ttl), sortable: true },
    { key: "values", label: "Values", value: (r) => r.records.map((x) => x.value).join("\n") },
    { key: "labels", label: "Labels", value: (r) => labelText(r.labels) },
    { key: "menu", label: "Actions", value: () => "" },
  ];

  async function load() {
    try {
      rows = await listAll<RRSet>(project, `/zones/${zoneId}/rrsets`, "rrsets");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load records", err);
      failing = true;
    }
  }

  // The records refresh every 5 s while the tab is shown.
  $effect(() => {
    void zoneId;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const rowEntries = [
    tabs.rrset_change_ttl,
    tabs.rrset_add_records,
    tabs.rrset_remove_records,
    tabs.rrset_labels,
    tabs.rrset_change_protection,
    tabs.rrset_delete,
  ];

  // rowActions binds the entries to the row's RRSet; the SOA keeps only Change TTL.
  function rowActions(r: RRSet): ResourceAction[] {
    const entries = r.type === "SOA" ? [tabs.rrset_change_ttl] : rowEntries;
    const at = (i: Input) => ({ ...i, rr_name: r.name, rr_type: r.type });
    return entries.map((a) => ({ ...a, path: (id, i) => a.path(id, at(i)), body: a.body && ((i, id) => a.body!(at(i), id)) }));
  }

  function changed() {
    load();
    onchanged();
  }

  // "Add record set" opens a dialog; the request runs after it closed.
  let addOpen = $state(false);

  async function add(i: Input) {
    addOpen = false;
    const a = tabs.create_rrset;
    const req = request(a, zone.id, i)!;
    const res = await runOn(project, `${i.name} ${i.type}`, a.label, req);
    if (res !== undefined) changed();
  }
</script>

<Table
  title="Records"
  {rows}
  {columns}
  key={(r) => r.id}
  name={(r) => r.name}
  labels={(r) => r.labels}
  emptyText="This zone has no records yet."
  createLabel="Add record set"
  oncreate={() => (addOpen = true)}
>
  {#snippet cell(r: RRSet, c: Column<RRSet>)}
    {#if c.key === "menu"}
      <ResourceActionsMenu {project} id={zone.id} name={`${r.name} ${r.type}`} actions={rowActions(r)} ondone={changed}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          <RRSetInput action={a} rrset={r} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {:else if c.key === "values"}
      {#each r.records as x (x.value)}<div class="mono">{x.value}</div>{/each}
    {:else}
      {c.value(r)}
    {/if}
  {/snippet}
</Table>

<Dialog bind:open={addOpen} title={`Add record set ${zone.name}`}>
  {#if addOpen}
    <RRSetInput action={tabs.create_rrset} submit={add} cancel={() => (addOpen = false)} />
  {/if}
</Dialog>

<style>
  .mono {
    font-family: var(--mono);
    overflow-wrap: anywhere;
  }
</style>
