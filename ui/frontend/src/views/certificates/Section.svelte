<script module lang="ts">
  /** Certificate as the Hetzner API returns it (the fields the views use). */
  export type Certificate = {
    id: number;
    name: string;
    type: "uploaded" | "managed";
    domain_names: string[];
    fingerprint: string | null;
    not_valid_before: string | null;
    not_valid_after: string | null;
    status: { issuance: string; renewal: string; error: { code: string; message: string } | null } | null;
    used_by: { id: number; type: string }[];
    labels: Record<string, string>;
    created: string;
  };

  /** statusText is a certificate's state: the issuance of a managed one, "uploaded" otherwise. */
  export function statusText(c: Certificate): string {
    return c.type === "managed" ? (c.status?.issuance ?? "pending") : "uploaded";
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
  import { tabs } from "../../lib/resources/certificates";
  import { showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import CertificateCreate from "./CertificateCreate.svelte";
  import CertificateDetail from "./CertificateDetail.svelte";

  // The "Certificates" section of a project console; page says which of its
  // pages the route opens (list, create form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "certificates", page: { kind: "list" } }));

  let rows = $state<Certificate[] | undefined>();
  let failing = false;

  const columns: Column<Certificate>[] = [
    { key: "name", label: "Name", value: (c) => c.name, sortable: true },
    { key: "type", label: "Type", value: (c) => c.type, sortable: true },
    { key: "domains", label: "Domains", value: (c) => c.domain_names.join(", ") },
    { key: "status", label: "Status", value: statusText, sortable: true },
    {
      key: "valid_until",
      label: "Valid until",
      value: (c) => (c.not_valid_after ? createdText(c.not_valid_after) : "—"),
      sortable: true,
    },
    { key: "labels", label: "Labels", value: (c) => labelText(c.labels) },
    { key: "created", label: "Created", value: (c) => createdText(c.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<Certificate>(project, "/certificates", "certificates");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load certificates", err);
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
    location.hash = href({ area: "project", project, view: "certificates", page: p });
  }

  // Row menu: Delete after a plain confirmation.
  let pending = $state<Certificate | undefined>();
  let confirmOpen = $state(false);

  function ask(c: Certificate) {
    pending = c;
    confirmOpen = true;
  }

  function run() {
    if (!pending) return;
    const c = pending;
    const req = request(tabs.delete, c.id)!;
    // Not awaited: the dialog closes at once, the toast follows the request.
    runOn(project, c.name, tabs.delete.label, req).then(load);
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>Certificates</h1>
    <HelpButton view="certificates" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <CertificateCreate {project} {list} />
{:else if page.kind === "detail"}
  <CertificateDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>Certificates</h1>
    <HelpButton view="certificates" />
  </div>
  <Table
    title="Certificates"
    {rows}
    {columns}
    key={(c) => c.id}
    name={(c) => c.name}
    labels={(c) => c.labels}
    emptyText="This project has no certificates yet."
    createLabel="Add certificate"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(c) => open({ kind: "detail", id: c.id, tab: "overview" })}
    actions={(c) => [{ label: "Delete", onclick: () => ask(c) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete certificate" confirmLabel="Delete" onconfirm={run}>
  {#if pending}
    <p>
      The certificate <strong>{pending.name}</strong> is deleted for good; a certificate a load balancer service uses
      cannot be deleted.
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
