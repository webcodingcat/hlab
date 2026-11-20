<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { api } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/zones";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import { modeText, type Zone } from "./Section.svelte";
  import ZoneActionInput from "./ZoneActionInput.svelte";
  import Records from "./tabs/Records.svelte";

  // The detail page of one DNS zone: Overview (mode, nameservers, default
  // TTL, record count, created), Records (primary zones only), Labels and
  // Protection; the actions menu changes the default TTL, imports a zone
  // file, changes the protection and deletes the zone (back to the list).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  const path = $derived(`/zones/${id}`);

  let zone = $state<Zone | undefined>();
  let notFound = $state(false);
  let failing = false;

  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      ...(zone?.mode === "secondary" ? [] : [{ id: "records", label: "Records" }]),
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "dns", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  async function load() {
    try {
      zone = (await api<{ zone: Zone }>(project, "GET", path)).zone;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load zone", err);
      failing = true;
    }
  }

  // The open zone refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      zone = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // A secondary zone holds no records of its own: no TTL, no zone file.
  const menuActions = $derived(
    menu.map((a): ResourceAction => {
      if (zone?.mode === "secondary" && (a.id === "change_ttl" || a.id === "import_zonefile")) {
        return { ...a, disabledHint: "A secondary zone takes its records from its primary nameservers." };
      }
      return a;
    }),
  );

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Zone" help="dns" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !zone}
  <p class="muted" aria-busy="true">Loading the zone…</p>
{:else}
  {@const z = zone}
  <DetailPage title={z.name} help="dns" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={z.id} name={z.name} actions={menuActions} ondone={done}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          <ZoneActionInput action={a} zone={z} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "records"}
      <Records {project} zone={z} onchanged={load} />
    {:else if current === "labels"}
      <LabelsTab {project} {path} labels={z.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} flags={[{ key: "delete", label: "Protect against deletion", value: z.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>Mode</dt>
        <dd>{modeText(z.mode)}</dd>
        <dt>Nameservers</dt>
        <dd>
          {#each z.authoritative_nameservers.assigned as ns (ns)}<div class="mono">{ns}</div>{/each}
        </dd>
        <dt>Default TTL</dt>
        <dd>{z.ttl} s</dd>
        <dt>Records</dt>
        <dd>{z.record_count}</dd>
        <dt>Created</dt>
        <dd>{createdText(z.created)}</dd>
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
  }
  .mono {
    font-family: var(--mono);
  }
  .muted {
    color: var(--muted);
  }
</style>
