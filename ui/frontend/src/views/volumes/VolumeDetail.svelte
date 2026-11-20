<script lang="ts">
  import { untrack } from "svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import RenameInput from "../../lib/RenameInput.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import StatusPill from "../../lib/StatusPill.svelte";
  import { api, listAll } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/volumes";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import type { Volume } from "./Section.svelte";
  import VolumeActionInput from "./VolumeActionInput.svelte";

  // The detail page of one volume: Overview (size, location, server, status,
  // Linux device, created), Labels and Protection; the actions menu resizes,
  // attaches, detaches, changes the protection and deletes it (back to the
  // list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Server = { id: number; name: string; location: { name: string } };

  const path = $derived(`/volumes/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "volumes", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let volume = $state<Volume | undefined>();
  let servers = $state<Server[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [v, s] = await Promise.all([
        api<{ volume: Volume }>(project, "GET", path),
        listAll<Server>(project, "/servers", "servers"),
      ]);
      volume = v.volume;
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load volume", err);
      failing = true;
    }
  }

  // The open volume refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      volume = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const tone = (status: string) => (status === "available" ? "success" : "warning");
  const serverName = (sid: number) => servers.find((s) => s.id === sid)?.name ?? `${sid}`;

  // Attach is disabled while the volume is attached, Detach while it is not.
  const menuActions = $derived(
    menu.map((a): ResourceAction => {
      if (a.id === "attach" && volume?.server != null) return { ...a, disabledHint: "Detach the volume first." };
      if (a.id === "detach" && volume?.server == null) return { ...a, disabledHint: "The volume is not attached." };
      return a;
    }),
  );

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Volume" help="volumes" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !volume}
  <p class="muted" aria-busy="true">Loading the volume…</p>
{:else}
  {@const v = volume}
  <DetailPage title={v.name} status={{ tone: tone(v.status), text: v.status }} help="volumes" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={v.id} name={v.name} actions={menuActions} ondone={done}>
        {#snippet input(a: ResourceAction, submit: (i: Input) => void, cancel: () => void)}
          {#if a.id === "rename"}
            <RenameInput current={v.name} {submit} {cancel} />
          {:else}
            <VolumeActionInput action={a} volume={v} {servers} {submit} {cancel} />
          {/if}
        {/snippet}
      </ResourceActionsMenu>
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={v.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab {project} {path} flags={[{ key: "delete", label: "Protect against deletion", value: v.protection.delete }]} onchanged={load} />
    {:else}
      <dl>
        <dt>Size</dt>
        <dd>{v.size} GB</dd>
        <dt>Location</dt>
        <dd>{v.location.name}</dd>
        <dt>Server</dt>
        <dd>
          {#if v.server === null}
            <span class="muted">Not attached</span>
          {:else}
            <a href={href({ area: "project", project, view: "servers", page: { kind: "detail", id: v.server, tab: "overview" } })}
              >{serverName(v.server)}</a
            >
          {/if}
        </dd>
        <dt>Status</dt>
        <dd><StatusPill tone={tone(v.status)} text={v.status} /></dd>
        <dt>Linux device</dt>
        <dd><CopyField value={v.linux_device} label="device path" /></dd>
        <dt>Created</dt>
        <dd>{createdText(v.created)}</dd>
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
  .muted {
    color: var(--muted);
  }
</style>
