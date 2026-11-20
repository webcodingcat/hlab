<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/networks";

  // The create form of a network: name, IP range and labels. Subnets and
  // routes are added on the network's page. The list opens as soon as the
  // API answers; the toast follows the request.
  let { project, list }: { project: string; list: string } = $props();

  let name = $state("");
  let ipRange = $state("10.0.0.0/16");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["name", "ip_range", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const networkName = name.trim();
    const req = request(create, 0, { name: networkName, ip_range: ipRange.trim(), labels: l.labels })!;
    busy = true;
    await runAction(
      actionTitle(resourceType(req.path), networkName, "Create"),
      async () => {
        try {
          const res = await api<ActionResult>(project, req.method, req.path, req.body);
          window.location.hash = list;
          return res;
        } catch (err) {
          errors = formErrors(err, known);
          throw err;
        } finally {
          busy = false;
        }
      },
      { get: async (id) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action },
    );
  }
</script>

<FormPage
  title="Create network"
  help="networks"
  submitLabel="Create network"
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
  <Field
    label="IP range"
    error={errors.fields.ip_range}
    hint="A private IPv4 range in CIDR notation, at least /24, for example 10.0.0.0/16."
  >
    {#snippet children(c)}
      <input
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        autocomplete="off"
        spellcheck="false"
        bind:value={ipRange}
      />
    {/snippet}
  </Field>
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  input {
    width: 100%;
  }
</style>
