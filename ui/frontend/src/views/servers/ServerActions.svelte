<script lang="ts">
  import ActionsMenu from "../../lib/ActionsMenu.svelte";
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import SecretDialog from "../../lib/SecretDialog.svelte";
  import type { ActionResult } from "../../lib/actions";
  import { listAll, runOn } from "../../lib/api";
  import { openConsoleWindow } from "../../lib/bridge";
  import { menuActions, request, type Input, type ServerAction } from "../../lib/serverActions";
  import { showError } from "../../lib/toast";
  import {
    imageGroups,
    imageLabel,
    typeLabel,
    typesIn,
    usableImages,
    type Image,
    type Named,
    type Server,
    type ServerType,
  } from "./servers";

  // The actions menu of a server's detail page. Entries with input open a
  // dialog first, destructive ones a confirmation; a root password in the
  // answer shows once. After Delete the list opens.
  type Props = { project: string; server: Server; list: string; onchanged: () => void };

  let { project, server, list, onchanged }: Props = $props();

  type Answer = ActionResult & { root_password?: string | null };

  // The action whose input dialog or confirmation is open, with its input.
  let pending = $state<{ action: ServerAction; input: Input } | undefined>();
  let inputOpen = $state(false);
  let confirmOpen = $state(false);
  let password = $state<string | undefined>();
  let secretOpen = $state(false);

  // Input dialog values and their options, loaded when a dialog opens.
  let image = $state("");
  let serverType = $state("");
  let upgradeDisk = $state(false);
  let name = $state("");
  let placementGroup = $state("");
  let images = $state<Image[]>([]);
  let types = $state<ServerType[]>([]);
  let groups = $state<Named[]>([]);

  const typeOptions = $derived(typesIn(types, server.location.name).filter((t) => t.name !== server.server_type.name));

  async function loadOptions(id: string) {
    try {
      if (id === "rebuild") images = usableImages(await listAll<Image>(project, "/images", "images"));
      if (id === "change_type") types = await listAll<ServerType>(project, "/server_types", "server_types");
      if (id === "add_to_placement_group") groups = await listAll<Named>(project, "/placement_groups", "placement_groups");
    } catch (err) {
      showError("Load the options", err);
    }
  }

  async function run(a: ServerAction, input: Input) {
    const req = request(a, server.id, input);
    if (!req) return;
    const res = await runOn<Answer>(project, server.name, a.label, req);
    if (res === undefined) return;
    if (a.id === "delete") {
      location.hash = list;
      return;
    }
    if (a.password && res.root_password) {
      password = res.root_password;
      secretOpen = true;
    }
    onchanged();
  }

  // proceed confirms the action if it needs that, or runs it.
  function proceed(a: ServerAction, input: Input) {
    if (a.confirm === "none") {
      run(a, input);
      return;
    }
    pending = { action: a, input };
    confirmOpen = true;
  }

  function choose(a: ServerAction) {
    if (a.id === "console") {
      openConsoleWindow(project, server.id, server.name).catch((err) => showError("Open the console", err));
      return;
    }
    if (!a.needsInput) {
      proceed(a, {});
      return;
    }
    image = serverType = placementGroup = "";
    upgradeDisk = false;
    name = server.name;
    pending = { action: a, input: {} };
    inputOpen = true;
    loadOptions(a.id);
  }

  function inputOf(id: string): Input {
    if (id === "rebuild") return { image: Number(image) };
    if (id === "change_type") return { server_type: serverType, upgrade_disk: upgradeDisk };
    if (id === "rename") return { name: name.trim() };
    if (id === "add_to_placement_group") return { placement_group: Number(placementGroup) };
    return {};
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!pending) return;
    const a = pending.action;
    inputOpen = false;
    proceed(a, inputOf(a.id));
  }

  const ready = $derived.by(() => {
    switch (pending?.action.id) {
      case "rebuild":
        return image !== "";
      case "change_type":
        return serverType !== "";
      case "rename":
        return name.trim() !== "" && name.trim() !== server.name;
      case "add_to_placement_group":
        return placementGroup !== "";
    }
    return true;
  });

  const items = $derived(
    menuActions.map((a) => ({
      label: a.label,
      danger: a.id === "delete",
      disabled: a.disabledHint !== undefined,
      hint: a.disabledHint,
      onclick: () => choose(a),
    })),
  );

  function closeSecret(open: boolean) {
    secretOpen = open;
    if (!open) password = undefined;
  }

  const imageName = (id: unknown) => {
    const i = images.find((x) => x.id === id);
    return i ? imageLabel(i) : `image ${id}`;
  };
</script>

<ActionsMenu {items} />

<Dialog bind:open={inputOpen} title={`${pending?.action.label ?? ""} ${server.name}`}>
  <form onsubmit={submit}>
    {#if pending?.action.id === "rebuild"}
      <Field label="Image" hint="Rebuilding replaces the server's disk with this image.">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={image}>
            <option value="" disabled>Choose an image</option>
            {#each imageGroups(images) as g (g.label)}
              {#if g.images.length > 0}
                <optgroup label={g.label}>
                  {#each g.images as i (i.id)}<option value={`${i.id}`}>{imageLabel(i)}</option>{/each}
                </optgroup>
              {/if}
            {/each}
          </select>
        {/snippet}
      </Field>
    {:else if pending?.action.id === "change_type"}
      <Field label="Type">
        {#snippet children(c)}
          <select id={c.id} bind:value={serverType}>
            <option value="" disabled>Choose a type</option>
            {#each typeOptions as t (t.id)}<option value={t.name}>{typeLabel(t)}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <label class="check">
        <input type="checkbox" role="switch" aria-checked={upgradeDisk} bind:checked={upgradeDisk} />Upgrade disk
      </label>
      <p class="hint">A disk upgrade cannot be undone: the server can no longer change to a type with a smaller disk.</p>
    {:else if pending?.action.id === "rename"}
      <Field label="Name">
        {#snippet children(c)}
          <input id={c.id} autocomplete="off" bind:value={name} />
        {/snippet}
      </Field>
    {:else if pending?.action.id === "add_to_placement_group"}
      <Field label="Placement group">
        {#snippet children(c)}
          <select id={c.id} bind:value={placementGroup}>
            <option value="" disabled>Choose a placement group</option>
            {#each groups as g (g.id)}<option value={`${g.id}`}>{g.name}</option>{/each}
          </select>
        {/snippet}
      </Field>
      {#if groups.length === 0}<p class="hint">This project has no placement groups.</p>{/if}
    {/if}
    <div class="actions">
      <Button onclick={() => (inputOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={!ready}>{pending?.action.label ?? ""}</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} server`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? server.name : undefined}
  onconfirm={() => {
    if (pending) run(pending.action, pending.input);
  }}
>
  {#if pending?.action.id === "delete"}
    <p>The server <strong>{server.name}</strong> and its disk are deleted for good.</p>
  {:else if pending?.action.id === "rebuild"}
    <p>
      The disk of <strong>{server.name}</strong> is replaced with {imageName(pending.input.image)}. All data on it is lost.
    </p>
  {:else if pending?.action.id === "change_type"}
    <p>
      <strong>{server.name}</strong> changes to type {pending.input.server_type}.
      {#if pending.input.upgrade_disk}The disk upgrade cannot be undone.{:else}The disk keeps its size, so the server can change back.{/if}
    </p>
  {:else if pending}
    <p>{pending.action.label} <strong>{server.name}</strong>? Data not yet written to its disk can be lost.</p>
  {/if}
</ConfirmDialog>

<SecretDialog bind:open={() => secretOpen, closeSecret} title="Root password" label="password" value={password} />

<style>
  form {
    display: flex;
    flex-direction: column;
  }
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .hint {
    margin: var(--s2) 0 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 16px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
