<script lang="ts">
  import type { Snippet } from "svelte";
  import ActionsMenu from "./ActionsMenu.svelte";
  import ConfirmDialog from "./ConfirmDialog.svelte";
  import Dialog from "./Dialog.svelte";
  import { runHetznerOn, runOn } from "./api";
  import { request, type Input, type ResourceAction } from "./resourceActions";

  // The actions menu of a resource's detail page or list row, built from
  // ResourceAction entries. An entry with input opens a dialog with the
  // input snippet first (it gets the entry, submit and cancel); then a
  // confirmation by the entry's confirm (typed-name asks for name); then
  // the request runs under the toast "<Type> <name>: <label>". Every dialog closes
  // at once; ondone follows when the request and its actions succeeded.
  // An entry for which onselect returns true is left to the page (for
  // example Reverse DNS with its own dialog). hetzner sends the requests to
  // the Hetzner API (storage boxes) instead of the Cloud API.
  type Props = {
    project: string;
    id: number;
    name: string;
    actions: ResourceAction[];
    input?: Snippet<[ResourceAction, (i: Input) => void, () => void]>;
    ondone: (a: ResourceAction) => void;
    onselect?: (a: ResourceAction) => boolean;
    hetzner?: boolean;
  };

  let { project, id, name, actions, input, ondone, onselect, hetzner = false }: Props = $props();

  // The entry whose input dialog or confirmation is open, with its input.
  let pending = $state<{ action: ResourceAction; input: Input } | undefined>();
  let inputOpen = $state(false);
  let confirmOpen = $state(false);

  async function run(a: ResourceAction, i: Input) {
    const req = request(a, id, i);
    if (!req) return;
    const res = await (hetzner ? runHetznerOn : runOn)(project, name, a.label, req);
    if (res !== undefined) ondone(a);
  }

  // proceed asks for the confirmation the entry needs, or runs it.
  function proceed(a: ResourceAction, i: Input) {
    if (a.confirm === "none") {
      run(a, i);
      return;
    }
    pending = { action: a, input: i };
    confirmOpen = true;
  }

  function choose(a: ResourceAction) {
    if (onselect?.(a)) return;
    if (a.needsInput && input) {
      pending = { action: a, input: {} };
      inputOpen = true;
      return;
    }
    proceed(a, {});
  }

  function submit(i: Input) {
    if (!pending) return;
    const a = pending.action;
    inputOpen = false;
    proceed(a, i);
  }

  function cancel() {
    inputOpen = false;
  }

  const items = $derived(
    actions.map((a) => ({
      label: a.label,
      danger: a.id === "delete",
      disabled: a.disabledHint !== undefined,
      hint: a.disabledHint,
      onclick: () => choose(a),
    })),
  );
</script>

<ActionsMenu {items} />

{#if input}
  <Dialog bind:open={inputOpen} title={`${pending?.action.label ?? ""} ${name}`}>
    {#if pending && inputOpen}{@render input(pending.action, submit, cancel)}{/if}
  </Dialog>
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={`${pending?.action.label ?? ""} ${name}`}
  confirmLabel={pending?.action.label ?? ""}
  typedName={pending?.action.confirm === "typed-name" ? name : undefined}
  onconfirm={() => {
    // Not awaited: the dialog closes at once, the toast follows the request.
    if (pending) run(pending.action, pending.input);
  }}
>
  {#if pending?.action.id === "delete"}
    <p><strong>{name}</strong> is deleted for good.</p>
  {:else if pending}
    <p>{pending.action.label} <strong>{name}</strong>?</p>
  {/if}
</ConfirmDialog>
