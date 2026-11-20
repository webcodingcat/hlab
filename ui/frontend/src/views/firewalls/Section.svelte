<script module lang="ts">
  import type { Rule } from "../../lib/resources/firewallRules";

  /** Firewall as the Hetzner API returns it (the fields the views use). */
  export type Firewall = {
    id: number;
    name: string;
    rules: Rule[];
    applied_to: {
      type: "server" | "label_selector";
      server?: { id: number };
      label_selector?: { selector: string };
      applied_to_resources?: { type: string; server: { id: number } }[];
    }[];
    labels: Record<string, string>;
    created: string;
  };

  /** appliedText counts the servers and label selectors a firewall applies to. */
  function appliedText(f: Firewall): string {
    const servers = f.applied_to.filter((a) => a.type === "server").length;
    const selectors = f.applied_to.length - servers;
    const parts = [];
    if (servers > 0) parts.push(`${servers} server${servers === 1 ? "" : "s"}`);
    if (selectors > 0) parts.push(`${selectors} label selector${selectors === 1 ? "" : "s"}`);
    return parts.join(", ") || "-";
  }
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { createdText, labelText } from "../../lib/format";
  import { request } from "../../lib/resourceActions";
  import { tabs } from "../../lib/resources/firewalls";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import FirewallCreate from "./FirewallCreate.svelte";
  import FirewallDetail from "./FirewallDetail.svelte";

  // The "Firewalls" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "firewalls", page: { kind: "list" } }));

  let rows = $state<Firewall[] | undefined>();
  let failing = false;

  const count = (f: Firewall, d: "in" | "out") => f.rules.filter((r) => r.direction === d).length;

  const columns: Column<Firewall>[] = [
    { key: "name", label: "Name", value: (f) => f.name, sortable: true },
    { key: "rules", label: "Rules in/out", value: (f) => `${count(f, "in")} / ${count(f, "out")}` },
    { key: "applied_to", label: "Applied to", value: appliedText },
    { key: "labels", label: "Labels", value: (f) => labelText(f.labels) },
    { key: "created", label: "Created", value: (f) => createdText(f.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Firewall>(project, "/firewalls", "firewalls");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load firewalls", err);
      failing = true;
    }
  }

  // The list refreshes every 5 s while it is shown.
  $effect(() => {
    if (!$running || page.kind !== "list") return;
    rows = undefined;
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  function open(p: Page) {
    location.hash = href({ area: "project", project, view: "firewalls", page: p });
  }

  // Row menu: Delete after a plain confirmation.
  let pending = $state<Firewall | undefined>();
  let confirmOpen = $state(false);

  function ask(f: Firewall) {
    pending = f;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const f = pending;
    const req = request(tabs.delete, f.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, f.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Firewalls</h1>
    <HelpButton view="firewalls" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <FirewallCreate {project} {list} />
{:else if page.kind === "detail"}
  <FirewallDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Firewalls</h1>
    <HelpButton view="firewalls" />
  </div>
  <Table
    title="Firewalls"
    {rows}
    {columns}
    key={(f) => f.id}
    name={(f) => f.name}
    labels={(f) => f.labels}
    emptyText="This project has no firewalls yet."
    createLabel="Create firewall"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(f) => open({ kind: "detail", id: f.id, tab: "rules" })}
    actions={(f) => [{ label: "Delete", onclick: () => ask(f) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete firewall" confirmLabel="Delete" onconfirm={run}>
  {#if pending}
    <p>
      The firewall <strong>{pending.name}</strong> is deleted for good.
    </p>
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
