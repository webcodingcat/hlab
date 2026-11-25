<script module lang="ts">
  import { actionTitle, createdText, labelText, resourceType } from "../../lib/format";

  /** SshKey as the Hetzner API returns it. */
  export type SshKey = {
    id: number;
    name: string;
    fingerprint: string;
    public_key: string;
    labels: Record<string, string>;
    created: string;
  };

  export { createdText, labelText };
</script>

<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import Table, { type Column } from "../../lib/Table.svelte";
  import { api, listAll } from "../../lib/api";
  import { action, showError } from "../../lib/toast";
  import { href, running, type Page } from "../../state";
  import SshKeyCreate from "./SshKeyCreate.svelte";
  import SshKeyDetail from "./SshKeyDetail.svelte";

  // The "SSH Keys" section of a project console; page says which of its
  // pages the route opens (list, add form or detail).
  let { project, page }: { project: string; page: Page } = $props();

  const list = $derived(href({ area: "project", project, view: "ssh-keys", page: { kind: "list" } }));

  let rows = $state<SshKey[] | undefined>();
  let failing = false;

  const columns: Column<SshKey>[] = [
    { key: "name", label: "Name", value: (k) => k.name, sortable: true },
    { key: "fingerprint", label: "Fingerprint", value: (k) => k.fingerprint, sortable: true },
    { key: "labels", label: "Labels", value: (k) => labelText(k.labels) },
    { key: "created", label: "Created", value: (k) => createdText(k.created), sortable: true },
  ];

  async function load() {
    try {
      rows = await listAll<SshKey>(project, "/ssh_keys", "ssh_keys");
      failing = false;
    } catch (err) {
      if (!failing) showError("Load SSH keys", err);
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
    location.hash = href({ area: "project", project, view: "ssh-keys", page: p });
  }

  // Delete with a plain confirmation.
  let deleting = $state<SshKey | undefined>();
  let confirmOpen = $state(false);

  async function remove() {
    const k = deleting!;
    const path = `/ssh_keys/${k.id}`;
    await action(actionTitle(resourceType(path), k.name, "Delete"), () => api(project, "DELETE", path));
    await load();
  }
</script>

{#if !$running}
  <div class="title-row">
    <h1>SSH Keys</h1>
    <HelpButton view="ssh-keys" />
  </div>
  <CloudStopped />
{:else if page.kind === "new"}
  <SshKeyCreate {project} {list} />
{:else if page.kind === "detail"}
  <SshKeyDetail {project} id={page.id} tab={page.tab} {list} />
{:else}
  <div class="title-row">
    <h1>SSH Keys</h1>
    <HelpButton view="ssh-keys" />
  </div>
  <Table
    title="SSH keys"
    {rows}
    {columns}
    key={(k) => k.id}
    name={(k) => k.name}
    labels={(k) => k.labels}
    emptyText="This project has no SSH keys yet."
    createLabel="Add SSH key"
    oncreate={() => open({ kind: "new" })}
    onrowclick={(k) => open({ kind: "detail", id: k.id, tab: "overview" })}
    actions={(k) => [{ label: "Delete", onclick: () => ((deleting = k), (confirmOpen = true)) }]}
  />
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete SSH key" confirmLabel="Delete" typedName={deleting?.name} onconfirm={remove}>
  <p>The SSH key <strong>{deleting?.name}</strong> is deleted. Servers that already use it keep it.</p>
</ConfirmDialog>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
</style>
