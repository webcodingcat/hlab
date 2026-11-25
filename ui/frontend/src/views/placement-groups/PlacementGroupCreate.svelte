<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runOn } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/placementGroups";

  // The create form of a placement group: the name, the type (a select with
  // spread, the only one) and labels. The list opens as soon as the API
  // answers.
  let { project, list }: { project: string; list: string } = $props();

  let name = $state("");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const groupName = name.trim();
    const req = request(create, 0, { name: groupName, labels: l.labels })!;
    busy = true;
    await runOn(project, groupName, "Create", req, {
      onanswer: () => {
        busy = false;
        window.location.hash = list;
      },
      onerror: (err) => {
        busy = false;
        errors = formErrors(err, ["name", "type", "labels"]);
      },
    });
  }
</script>

<FormPage
  title="Create placement group"
  help="placement-groups"
  submitLabel="Create placement group"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <Field label="Type" error={errors.fields.type} hint="Spread: each server of the group runs on its own host (in HLab, the group only records its servers).">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid}>
        <option value="spread" selected>spread</option>
      </select>
    {/snippet}
  </Field>
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  input,
  select {
    width: 100%;
  }
</style>
