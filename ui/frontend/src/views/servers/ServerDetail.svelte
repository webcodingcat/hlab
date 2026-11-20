<script lang="ts">
  import { untrack } from "svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ProtectionTab from "../../lib/ProtectionTab.svelte";
  import StatusPill from "../../lib/StatusPill.svelte";
  import { api } from "../../lib/api";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import ServerActions from "./ServerActions.svelte";
  import Backups from "./tabs/Backups.svelte";
  import Firewalls from "./tabs/Firewalls.svelte";
  import Iso from "./tabs/Iso.svelte";
  import Networking from "./tabs/Networking.svelte";
  import Rescue from "./tabs/Rescue.svelte";
  import Snapshots from "./tabs/Snapshots.svelte";
  import Volumes from "./tabs/Volumes.svelte";
  import { createdText, imageLabel, tone, type Server } from "./servers";

  // The detail page of one server: Overview, Networking, Firewalls,
  // Volumes, Backups, Snapshots, Rescue, ISO, Labels and Protection, with the actions menu in the title
  // row; refreshed every 5 s.
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  const path = $derived(`/servers/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "networking", label: "Networking" },
      { id: "firewalls", label: "Firewalls" },
      { id: "volumes", label: "Volumes" },
      { id: "backups", label: "Backups" },
      { id: "snapshots", label: "Snapshots" },
      { id: "rescue", label: "Rescue" },
      { id: "iso", label: "ISO" },
      { id: "labels", label: "Labels" },
      { id: "protection", label: "Protection" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "servers", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let server = $state<Server | undefined>();
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      server = (await api<{ server: Server }>(project, "GET", path)).server;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load server", err);
      failing = true;
    }
  }

  $effect(() => {
    void path;
    untrack(() => {
      server = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });
</script>

{#if notFound}
  <DetailPage title="Server" help="servers" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !server}
  <p class="muted" aria-busy="true">Loading the server…</p>
{:else}
  <DetailPage title={server.name} status={{ tone: tone(server.status), text: server.status }} help="servers" {tabs} {current} back={list}>
    {#snippet actions()}
      <ServerActions {project} server={server!} {list} onchanged={load} />
    {/snippet}
    {#if current === "networking"}
      <Networking {project} server={server!} onchanged={load} />
    {:else if current === "firewalls"}
      <Firewalls {project} server={server!} onchanged={load} />
    {:else if current === "volumes"}
      <Volumes {project} server={server!} onchanged={load} />
    {:else if current === "backups"}
      <Backups {project} server={server!} onchanged={load} />
    {:else if current === "snapshots"}
      <Snapshots {project} server={server!} onchanged={load} />
    {:else if current === "rescue"}
      <Rescue {project} server={server!} onchanged={load} />
    {:else if current === "iso"}
      <Iso {project} server={server!} onchanged={load} />
    {:else if current === "labels"}
      <LabelsTab {project} {path} labels={server.labels} onsaved={load} />
    {:else if current === "protection"}
      <ProtectionTab
        {project}
        {path}
        flags={[
          { key: "delete", label: "Protect against deletion", value: server.protection.delete },
          { key: "rebuild", label: "Protect against rebuild", value: server.protection.rebuild },
        ]}
        onchanged={load}
      />
    {:else}
      <dl>
        <dt>Status</dt>
        <dd><StatusPill tone={tone(server.status)} text={server.status} /></dd>
        <dt>Type</dt>
        <dd>{server.server_type.name}</dd>
        <dt>Location</dt>
        <dd>{server.location.name}</dd>
        <dt>Image</dt>
        <dd>{server.image ? imageLabel(server.image) : "-"}</dd>
        <dt>Public IPv4</dt>
        <dd>{#if server.public_net.ipv4}<CopyField value={server.public_net.ipv4.ip} label="IPv4" />{:else}-{/if}</dd>
        <dt>Public IPv6</dt>
        <dd>{#if server.public_net.ipv6}<CopyField value={server.public_net.ipv6.ip} label="IPv6" />{:else}-{/if}</dd>
        <dt>Private IPs</dt>
        <dd>
          {#each server.private_net as n (n.network)}
            <div class="mono">{[n.ip, ...n.alias_ips].join(", ")}</div>
          {:else}-{/each}
        </dd>
        <dt>Placement group</dt>
        <dd>{server.placement_group?.name ?? "-"}</dd>
        <dt>Created</dt>
        <dd>{createdText(server.created)}</dd>
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
