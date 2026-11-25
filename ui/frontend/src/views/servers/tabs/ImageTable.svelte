<script module lang="ts">
  /** ServerImage is a backup or snapshot as /images lists it. */
  export type ServerImage = {
    id: number;
    type: string;
    status: string;
    description: string;
    image_size: number | null;
    created: string;
    created_from: { id: number; name: string } | null;
    bound_to: number | null;
  };
</script>

<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import SecretDialog from "../../../lib/SecretDialog.svelte";
  import StatusPill from "../../../lib/StatusPill.svelte";
  import { createdText, type Server } from "../servers";
  import { runTab, runTabAnswer } from "./run";

  // The backups or snapshots of a server with Restore (rebuild from the
  // image, confirmed, root password shown once) and Delete (confirmed);
  // shared by the Backups and Snapshots tabs.
  type Props = { project: string; server: Server; kind: "backup" | "snapshot"; images: ServerImage[] | undefined; onchanged: () => void };

  let { project, server, kind, images, onchanged }: Props = $props();

  let pending = $state<{ id: "restore" | "delete_image"; image: ServerImage } | undefined>();
  let confirmOpen = $state(false);
  let password = $state<string | undefined>();
  let secretOpen = $state(false);

  const name = (i: ServerImage) => i.description || `${kind} ${i.id}`;

  function ask(id: "restore" | "delete_image", image: ServerImage) {
    pending = { id, image };
    confirmOpen = true;
  }

  async function run() {
    if (!pending) return;
    const { id, image } = pending;
    if (id === "delete_image") {
      if (await runTab(project, server, id, { image: image.id })) onchanged();
      return;
    }
    const res = await runTabAnswer(project, server, id, { image: image.id });
    if (res === undefined) return;
    if (res.root_password) {
      password = res.root_password;
      secretOpen = true;
    }
    onchanged();
  }

  function closeSecret(open: boolean) {
    secretOpen = open;
    if (!open) password = undefined;
  }
</script>

{#if !images}
  <p class="muted" aria-busy="true">Loading the {kind}s…</p>
{:else if images.length === 0}
  <p class="muted">This server has no {kind}s.</p>
{:else}
  <table>
    <thead>
      <tr><th>Name</th><th>Status</th><th>Size</th><th>Created</th><th><span class="hidden">Actions</span></th></tr>
    </thead>
    <tbody>
      {#each images as i (i.id)}
        <tr>
          <td>{name(i)}</td>
          <td><StatusPill tone={i.status === "available" ? "success" : "warning"} text={i.status} /></td>
          <td>{i.image_size === null ? "-" : `${i.image_size.toFixed(2)} GB`}</td>
          <td>{createdText(i.created)}</td>
          <td class="row-actions">
            <Button disabled={i.status !== "available"} onclick={() => ask("restore", i)}>Restore</Button>
            <Button onclick={() => ask("delete_image", i)}>Delete</Button>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={pending?.id === "restore" ? `Restore ${server.name}` : `Delete ${kind}`}
  confirmLabel={pending?.id === "restore" ? "Restore" : "Delete"}
  typedName={pending?.id === "delete_image" ? name(pending.image) : undefined}
  onconfirm={run}
>
  {#if pending?.id === "restore"}
    <p>The disk of <strong>{server.name}</strong> is replaced with the {kind} {name(pending.image)}. All data written since is lost.</p>
  {:else if pending}
    <p>The {kind} <strong>{name(pending.image)}</strong> is deleted for good.</p>
  {/if}
</ConfirmDialog>

<SecretDialog bind:open={() => secretOpen, closeSecret} title="Root password" label="password" value={password} />

<style>
  table {
    width: 100%;
    border-collapse: collapse;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  th,
  td {
    text-align: left;
    padding: var(--s2) var(--s3);
    border-bottom: 1px solid var(--border);
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 12px;
  }
  .row-actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
  .hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
</style>
