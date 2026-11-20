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
  import { toRules, type RuleRow } from "../../lib/resources/firewallRules";
  import { create } from "../../lib/resources/firewalls";
  import ApplyEditor from "./ApplyEditor.svelte";
  import RulesEditor from "./RulesEditor.svelte";

  // The create form of a firewall in the Hetzner Console's order: rules,
  // apply to (servers and label selectors), labels, name. The list opens as
  // soon as the API answers; the toast follows the request.
  let { project, list }: { project: string; list: string } = $props();

  let ruleRows = $state<RuleRow[]>([]);
  let servers = $state<number[]>([]);
  let selectors = $state<string[]>([]);
  let labelRows = $state<LabelRow[]>([]);
  let name = $state("");
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["name", "labels", "rules", "apply_to"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const firewallName = name.trim();
    const applyTo = [
      ...servers.map((id) => ({ type: "server", server: { id } })),
      ...selectors
        .map((s) => s.trim())
        .filter((s) => s !== "")
        .map((selector) => ({ type: "label_selector", label_selector: { selector } })),
    ];
    const req = request(create, 0, {
      name: firewallName,
      labels: l.labels,
      rules: toRules(ruleRows),
      apply_to: applyTo,
    })!;
    busy = true;
    await runAction(
      actionTitle(resourceType(req.path), firewallName, "Create"),
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
  title="Create firewall"
  help="firewalls"
  submitLabel="Create firewall"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <RulesEditor bind:rows={ruleRows} error={errors.fields.rules} />
  <ApplyEditor {project} bind:servers bind:selectors error={errors.fields.apply_to} />
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
</FormPage>

<style>
  input {
    width: 100%;
  }
</style>
