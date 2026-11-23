<script lang="ts">
  import Button from "../../lib/Button.svelte";
  import CopyField from "../../lib/CopyField.svelte";
  import Field from "../../lib/Field.svelte";
  import { hetznerApi } from "../../lib/api";
  import { bytesText } from "../../lib/format";
  import type { StorageBox } from "../../lib/resources/storageBoxes";

  // The Overview tab of a storage box: hostname and username as copy
  // fields, type, location, usage, and a folder browser. The path field
  // names a folder relative to the box's home ("" is the home); a click on
  // a folder descends into it, "Up" goes to the parent.
  let { project, box }: { project: string; box: StorageBox } = $props();

  const boxId = $derived(box.id);
  // Only a change of the status to or from active reloads the folders, not every refresh.
  const active = $derived(box.status === "active");

  let path = $state("");
  let draft = $state("");
  let folders = $state<string[] | undefined>();
  let problem = $state<string | undefined>();
  let seq = 0;

  // The folders of path load when the box, the path or whether the box is active changes.
  $effect(() => {
    void active;
    const p = path;
    const n = ++seq;
    folders = undefined;
    problem = undefined;
    const query = p === "" ? "" : `?path=${encodeURIComponent(p)}`;
    hetznerApi<{ folders: string[] }>(project, "GET", `/storage_boxes/${boxId}/folders${query}`)
      .then((r) => {
        if (n === seq) folders = [...r.folders].sort((a, b) => a.localeCompare(b));
      })
      .catch((err) => {
        if (n !== seq) return;
        const e = err as { code?: unknown; message?: unknown };
        problem =
          e.code === "not_found"
            ? "This folder does not exist."
            : e.code === "locked"
              ? "The folders can be listed once the storage box is active."
              : String(e.message ?? err);
      });
  });

  function go(p: string) {
    path = p;
    draft = p;
  }

  function open(e: SubmitEvent) {
    e.preventDefault();
    go(
      draft
        .split("/")
        .filter((s) => s !== "" && s !== ".")
        .join("/"),
    );
  }

  const up = () => go(path.split("/").slice(0, -1).join("/"));
  const descend = (f: string) => go(path === "" ? f : `${path}/${f}`);
</script>

<dl>
  <dt>Hostname</dt>
  <dd>
    {#if box.server}<CopyField value={box.server} label="hostname" />{:else}<span class="muted">Not yet assigned</span>{/if}
  </dd>
  <dt>Username</dt>
  <dd>
    {#if box.username}<CopyField value={box.username} label="username" />{:else}<span class="muted">Not yet assigned</span>{/if}
  </dd>
  <dt>Type</dt>
  <dd>{box.storage_box_type.name}</dd>
  <dt>Location</dt>
  <dd>{box.location.name}</dd>
  <dt>Usage</dt>
  <dd>
    used {bytesText(box.stats.size)} of {bytesText(box.storage_box_type.size)}
    <span class="muted">(files {bytesText(box.stats.size_data)}, snapshots {bytesText(box.stats.size_snapshots)})</span>
  </dd>
</dl>

<h2>Folders</h2>
<form class="path" onsubmit={open}>
  <Field label="Path" hint="Relative to the storage box's home; empty is the home.">
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={draft} />
    {/snippet}
  </Field>
  <div class="buttons">
    <Button type="submit">Open</Button>
    <Button onclick={up} disabled={path === ""}>Up</Button>
  </div>
</form>
{#if problem}
  <p class="muted">{problem}</p>
{:else if !folders}
  <p class="muted" aria-busy="true">Loading the folders…</p>
{:else if folders.length === 0}
  <p class="muted">No folders here.</p>
{:else}
  <ul class="folders">
    {#each folders as f (f)}
      <li><button type="button" class="folder" onclick={() => descend(f)}>{f}</button></li>
    {/each}
  </ul>
{/if}

<style>
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--s2) var(--s4);
    max-width: 640px;
    margin: 0 0 var(--s6);
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
  .path {
    display: flex;
    align-items: flex-start;
    gap: var(--s2);
    max-width: 640px;
  }
  .path :global(.field) {
    flex: 1;
  }
  .path input {
    width: 100%;
  }
  .buttons {
    display: flex;
    gap: var(--s2);
    margin-top: 24px;
  }
  .folders {
    max-width: 640px;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .folders li + li {
    border-top: 1px solid var(--border);
  }
  .folder {
    width: 100%;
    padding: var(--s2) var(--s3);
    border: none;
    background: none;
    color: var(--accent);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .folder:hover {
    background: var(--surface);
  }
  .folder:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
