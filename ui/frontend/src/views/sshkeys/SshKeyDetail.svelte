<script lang="ts">
  import { untrack } from "svelte";
  import ActionsMenu from "../../lib/ActionsMenu.svelte";
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import Field from "../../lib/Field.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import { api } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors } from "../../lib/formErrors";
  import { action, showError, showSuccess } from "../../lib/toast";
  import { href } from "../../state";
  import { createdText, type SshKey } from "./SshKeys.svelte";

  // The detail page of one SSH key: Overview (name, fingerprint, public
  // key, created) and Labels; the actions menu deletes it.
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  const path = $derived(`/ssh_keys/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "ssh-keys", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let key = $state<SshKey | undefined>();
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      key = (await api<{ ssh_key: SshKey }>(project, "GET", path)).ssh_key;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load SSH key", err);
      failing = true;
    }
  }

  // Name edit in the Overview.
  let name = $state("");
  // baseName is the last name shown; a field that still holds it is unedited.
  let baseName = "";
  let nameError = $state<string | undefined>();
  let general = $state<string | undefined>();
  let saving = $state(false);

  // The open key refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      key = undefined;
      notFound = false;
      name = baseName = "";
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // The refreshed name replaces the field as long as it is not edited.
  $effect(() => {
    if (!key) return;
    const next = key.name;
    untrack(() => {
      if (name === baseName) name = next;
      baseName = next;
    });
  });

  async function rename(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    nameError = general = undefined;
    try {
      const old = key!.name;
      key = (await api<{ ssh_key: SshKey }>(project, "PUT", path, { name: name.trim() })).ssh_key;
      name = key.name;
      showSuccess(actionTitle(resourceType(path), old, "Rename"), `SSH key renamed to ${key.name}`);
    } catch (err) {
      const fe = formErrors(err, ["name"]);
      nameError = fe.fields.name;
      general = fe.general;
    } finally {
      saving = false;
    }
  }

  // Delete with a plain confirmation; back to the list afterwards.
  let confirmOpen = $state(false);

  async function remove() {
    const k = key!;
    const done = await action(actionTitle(resourceType(path), k.name, "Delete"), () => api(project, "DELETE", path).then(() => true));
    if (done) location.hash = list;
  }
</script>

{#if notFound}
  <DetailPage title="SSH key" help="ssh-keys" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !key}
  <p class="muted" aria-busy="true">Loading the SSH key…</p>
{:else}
  <DetailPage title={key.name} help="ssh-keys" {tabs} {current} back={list}>
    {#snippet actions()}
      <ActionsMenu items={[{ label: "Delete", danger: true, onclick: () => (confirmOpen = true) }]} />
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={key.labels} onsaved={load} />
    {:else}
      <form class="name" onsubmit={rename}>
        <Field label="Name" error={nameError}>
          {#snippet children(c)}
            <div class="inline">
              <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
              <Button type="submit" loading={saving} disabled={name.trim() === key!.name}>Save name</Button>
            </div>
          {/snippet}
        </Field>
        {#if general}<p class="error" role="alert">{general}</p>{/if}
      </form>
      <dl>
        <dt>Fingerprint</dt>
        <dd class="mono">{key.fingerprint}</dd>
        <dt>Public key</dt>
        <dd><CopyField value={key.public_key} label="public key" /></dd>
        <dt>Created</dt>
        <dd>{createdText(key.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
{/if}

<ConfirmDialog bind:open={confirmOpen} title="Delete SSH key" confirmLabel="Delete" onconfirm={remove}>
  <p>The SSH key <strong>{key?.name}</strong> is deleted. Servers that already use it keep it.</p>
</ConfirmDialog>

<style>
  .name,
  dl {
    max-width: 640px;
  }
  .inline {
    display: flex;
    gap: var(--s2);
  }
  .inline input {
    flex: 1;
  }
  .error {
    color: var(--error);
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--s2) var(--s4);
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
