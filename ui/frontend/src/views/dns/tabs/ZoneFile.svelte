<script lang="ts">
  import { Check, Copy } from "@lucide/svelte";
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Field from "../../../lib/Field.svelte";
  import { runAction, type Action, type ActionResult } from "../../../lib/actions";
  import { api } from "../../../lib/api";
  import { formErrors } from "../../../lib/formErrors";
  import { request } from "../../../lib/resourceActions";
  import { menu, tabs } from "../../../lib/resources/zones";
  import { showError } from "../../../lib/toast";
  import type { Zone } from "../Section.svelte";

  // The Zone file tab of a primary zone: the export in a read-only text
  // area with a copy button, and an import from pasted text after a plain
  // confirmation. A bad zone file is refused at once with its line number,
  // shown under the import field.
  let { project, zone, onchanged }: { project: string; zone: Zone; onchanged: () => void } = $props();

  const zoneId = $derived(zone.id);
  const importAction = menu.find((a) => a.id === "import_zonefile")!;

  let exported = $state<string | undefined>();
  let copied = $state(false);
  let text = $state("");
  let error = $state<string | undefined>();
  let confirmOpen = $state(false);
  let busy = $state(false);

  async function load() {
    const req = request(tabs.export_zonefile, zoneId)!;
    try {
      exported = (await api<{ zonefile: string }>(project, req.method, req.path)).zonefile;
    } catch (err) {
      showError("Export zone file", err);
    }
  }

  $effect(() => {
    void zoneId;
    exported = undefined;
    load();
  });

  async function copy() {
    try {
      await navigator.clipboard.writeText(exported ?? "");
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch (err) {
      showError("Copy zone file", err);
    }
  }

  function ask(e: SubmitEvent) {
    e.preventDefault();
    if (text.trim() === "") return;
    confirmOpen = true;
  }

  // run sends the import; the dialog closes at once, a refusal of the zone
  // file lands under the field, the toast follows the action.
  async function run() {
    confirmOpen = false;
    error = undefined;
    busy = true;
    const req = request(importAction, zoneId, { zonefile: text })!;
    const res = await runAction(
      `${importAction.label} ${zone.name}`,
      async () => {
        try {
          return await api<ActionResult>(project, req.method, req.path, req.body);
        } catch (err) {
          const f = formErrors(err, ["zonefile"]);
          error = f.fields.zonefile ?? f.general;
          throw err;
        } finally {
          busy = false;
        }
      },
      { get: async (id) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action },
    );
    if (res === undefined) return;
    text = "";
    load();
    onchanged();
  }
</script>

<section>
  <div class="head">
    <h2>Export</h2>
    <Button onclick={copy} disabled={exported === undefined}>
      {#if copied}<Check size={16} aria-hidden="true" />{:else}<Copy size={16} aria-hidden="true" />{/if}Copy zone file
    </Button>
  </div>
  <textarea aria-label="Exported zone file" readonly rows="14" spellcheck="false" value={exported ?? ""}></textarea>
</section>

<section>
  <h2>Import</h2>
  <form onsubmit={ask}>
    <Field label="Zone file" {error} hint="A zone file in BIND format; it replaces all records of the zone.">
      {#snippet children(c)}
        <textarea
          id={c.id}
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          rows="10"
          spellcheck="false"
          bind:value={text}
        ></textarea>
      {/snippet}
    </Field>
    <div class="actions">
      <Button type="submit" variant="primary" loading={busy} disabled={text.trim() === ""}>Import zone file</Button>
    </div>
  </form>
</section>

<ConfirmDialog bind:open={confirmOpen} title={`Import zone file ${zone.name}`} confirmLabel="Import zone file" onconfirm={run}>
  <p>The zone file replaces all records of {zone.name}.</p>
</ConfirmDialog>

<style>
  section {
    max-width: 800px;
    margin-bottom: var(--s6);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s2);
  }
  h2 {
    font-size: 14px;
    margin: 0 0 var(--s2);
  }
  textarea {
    width: 100%;
    font-family: var(--mono);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
