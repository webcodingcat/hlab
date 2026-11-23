<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import StatusPill from "../../lib/StatusPill.svelte";
  import Table, { type Column, type RowAction } from "../../lib/Table.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { openConsoleWindow } from "../../lib/bridge";
  import { menuActions, request, type ServerAction } from "../../lib/serverActions";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import ServerCreate from "./ServerCreate.svelte";
  import ServerDetail from "./ServerDetail.svelte";
  import { createdText, tone, type Server } from "./servers";

  // The "Servers" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  let rows = $state<Server[] | undefined>();
  let failing = false;

  const listHref = $derived(href({ area: "project", project, view: "servers", page: { kind: "list" } }));
  const newHref = $derived(href({ area: "project", project, view: "servers", page: { kind: "new" } }));
  const detailHref = (id: number) =>
    href({ area: "project", project, view: "servers", page: { kind: "detail", id, tab: "overview" } });
  const created = (s: Server) => createdText(s.created);

  const labelText = (l: Record<string, string>) =>
    Object.keys(l)
      .sort()
      .map((k) => (l[k] ? `${k}=${l[k]}` : k))
      .join(", ");

  const columns: Column<Server>[] = [
    { key: "name", label: "Name", value: (s) => s.name, sortable: true },
    { key: "status", label: "Status", value: (s) => s.status, sortable: true },
    { key: "ipv4", label: "Public IPv4", value: (s) => s.public_net.ipv4?.ip ?? "", sortable: true },
    { key: "type", label: "Type", value: (s) => s.server_type.name, sortable: true },
    { key: "location", label: "Location", value: (s) => s.location.name, sortable: true },
    { key: "labels", label: "Labels", value: (s) => labelText(s.labels ?? {}) },
    { key: "created", label: "Created", value: created, sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Server>(project, "/servers", "servers");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load servers", err);
      failing = true;
    }
  }

  $effect(() => {
    if (!$running || page.kind !== "list") return;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // The row menu: the entries of the actions menu that need no input.
  const rowIds = ["poweron", "shutdown", "reboot", "reset", "poweroff", "console", "delete"];
  const rowEntries = menuActions.filter((a) => rowIds.includes(a.id));

  let pending = $state<{ action: ServerAction; server: Server } | undefined>();
  let confirmOpen = $state(false);

  async function run(a: ServerAction, s: Server) {
    const req = request(a, s.id);
    if (!req) return;
    await runOn(project, s.name, a.label, req);
    await load();
  }

  function choose(a: ServerAction, s: Server) {
    if (a.id === "console") {
      openConsoleWindow(project, s.id, s.name).catch((err) => showError("Open the console", err));
      return;
    }
    if (a.confirm === "none") {
      run(a, s);
      return;
    }
    pending = { action: a, server: s };
    confirmOpen = true;
  }

  const rowActions = (s: Server): RowAction[] =>
    rowEntries.map((a) => ({ label: a.label, onclick: () => choose(a, s) }));
</script>

{#if !$running}
  <div class="title-row">
    <h1>Servers</h1>
    <HelpButton view="servers" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <ServerCreate {project} />
{:else if page.kind === "detail"}
  <ServerDetail {project} id={page.id} tab={page.tab} list={listHref} />
{:else}
  <div class="title-row">
    <h1>Servers</h1>
    <HelpButton view="servers" />
  </div>
  <Table
    title="Servers"
    {rows}
    {columns}
    key={(s) => s.id}
    name={(s) => s.name}
    labels={(s) => s.labels ?? {}}
    emptyText="This project has no servers yet."
    createLabel="Create server"
    oncreate={() => (location.hash = newHref)}
    onrowclick={(s) => (location.hash = detailHref(s.id))}
    actions={rowActions}
  >
    {#snippet cell(s, c)}
      {#if c.key === "status"}<StatusPill tone={tone(s.status)} text={s.status} />{:else}{c.value(s)}{/if}
    {/snippet}
  </Table>
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} server`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? pending.server.name : undefined}
  onconfirm={() => {
    if (pending) run(pending.action, pending.server);
  }}
>
  {#if pending?.action.id === "delete"}
    <p>The server <strong>{pending.server.name}</strong> and its disk are deleted for good.</p>
  {:else if pending}
    <p>{pending.action.label} <strong>{pending.server.name}</strong>? Data not yet written to its disk can be lost.</p>
  {/if}
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
</style>
