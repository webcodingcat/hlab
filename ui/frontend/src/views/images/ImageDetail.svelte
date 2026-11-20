<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import StatusPill from "../../lib/StatusPill.svelte";
  import { api } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/images";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import ImageActionInput from "./ImageActionInput.svelte";
  import { imageName, sizeText, type Image } from "./ImageList.svelte";

  // The detail page of one snapshot or backup: Overview (type, status,
  // description, sizes, the server it was made from, created), Labels and
  // Protection; the actions menu edits the description, converts a backup
  // to a snapshot, changes the protection and deletes it (back to the list
  // afterwards). view is the section it was opened from (snapshots or
  // backups); its tabs, help and back link stay in that section.
  type Props = { project: string; view: "snapshots" | "backups"; id: number; tab: string; list: string };
  let { project, view, id, tab, list }: Props = $props();

  const path = $derived(`/images/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view, page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let image = $state<Image | undefined>();
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      image = (await api<{ image: Image }>(project, "GET", path)).image;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load image", err);
      failing = true;
    }
  }

  // The open image refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      image = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const serverHref = (sid: number) =>
    href({ area: "project", project, view: "servers", page: { kind: "detail", id: sid, tab: "overview" } });

  // Convert to snapshot is a fixed entry of a backup's menu only.
  const menuActions = $derived(menu.filter((a) => a.id !== "convert" || image?.type === "backup"));

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title={view === "snapshots" ? "Snapshot" : "Backup"} help={view} {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !image}
  <p class="muted" aria-busy="true">Loading the image…</p>
{:else}
  {@const i = image}
  <DetailPage title={imageName(i)} help={view} {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={i.id} name={imageName(i)} actions={menuActions} ondone={done}>
        {#snippet input(a: ResourceAction, submit: (x: Input) => void, cancel: () => void)}
          <ImageActionInput action={a} image={i} {submit} {cancel} />
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={i.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} name={imageName(i)} flags={[{ key: "delete", label: "Protect against deletion", value: i.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>Type</dt>
        <dd>{i.type}</dd>
        <dt>Status</dt>
        <dd><StatusPill tone={i.status === "available" ? "success" : "warning"} text={i.status} /></dd>
        <dt>Description</dt>
        <dd>
          {#if i.description}{i.description}{:else}<span class="muted">None</span>{/if}
        </dd>
        <dt>Size</dt>
        <dd>{sizeText(i)}</dd>
        <dt>Disk size</dt>
        <dd>{i.disk_size} GB</dd>
        <dt>Created from</dt>
        <dd>
          {#if i.created_from}<a href={serverHref(i.created_from.id)}>{i.created_from.name}</a>{:else}<span class="muted">-</span>{/if}
        </dd>
        <dt>Created</dt>
        <dd>{createdText(i.created)}</dd>
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
