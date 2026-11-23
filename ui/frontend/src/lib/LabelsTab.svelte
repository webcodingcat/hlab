<script lang="ts">
  import { untrack } from "svelte";
  import Button from "./Button.svelte";
  import LabelsField from "./LabelsField.svelte";
  import { api, hetznerApi } from "./api";
  import { formErrors } from "./formErrors";
  import { toLabels, toRows, type LabelRow } from "./labels";

  // The Labels tab of a detail page: edits the resource's labels and saves
  // them with PUT <path> {labels}; hetzner sends it to the Hetzner API
  // (storage boxes) instead of the Cloud API.
  type Props = { project: string; path: string; labels: Record<string, string>; onsaved: () => void; hetzner?: boolean };

  let { project, path, labels, onsaved, hetzner = false }: Props = $props();

  let rows = $state<LabelRow[]>([]);
  // base is the last state of the labels shown; rows that still equal it are unedited.
  let base = "[]";
  let saving = $state(false);
  let error = $state<string | undefined>();
  let general = $state<string | undefined>();

  // The refreshed labels replace the rows as long as nothing is edited.
  $effect(() => {
    const next = toRows(labels);
    untrack(() => {
      if (JSON.stringify(rows) === base) rows = next;
      base = JSON.stringify(next);
    });
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const r = toLabels(rows);
    if ("error" in r) return;
    saving = true;
    error = general = undefined;
    try {
      await (hetzner ? hetznerApi : api)(project, "PUT", path, { labels: r.labels });
      rows = toRows(r.labels);
      base = JSON.stringify(rows);
      onsaved();
    } catch (err) {
      const fe = formErrors(err, ["labels"]);
      error = fe.fields.labels;
      general = fe.general;
    } finally {
      saving = false;
    }
  }
</script>

<form onsubmit={save}>
  <LabelsField bind:rows {error} />
  {#if general}<p class="error" role="alert">{general}</p>{/if}
  <div class="actions">
    <Button type="submit" variant="primary" loading={saving}>Save labels</Button>
  </div>
</form>

<style>
  form {
    max-width: 640px;
  }
  .error {
    color: var(--error);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
