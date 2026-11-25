<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runOn } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/certificates";

  // The create form of a certificate: the type, then the name with the PEM
  // certificate and private key of an uploaded one, or the name with the
  // domain names of a managed one; labels. The list opens as soon as the
  // API answers; the toast follows the request (for a managed certificate
  // its issuance, with dns_zone_not_found when a domain lies in no zone).
  let { project, list }: { project: string; list: string } = $props();

  let type = $state<"uploaded" | "managed">("uploaded");
  let name = $state("");
  let certificate = $state("");
  let privateKey = $state("");
  let domains = $state("");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["type", "name", "certificate", "private_key", "domain_names", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const certName = name.trim();
    const input: Input = { type, name: certName, labels: l.labels };
    if (type === "managed") input.domain_names = domains.split(/[\s,]+/).filter((d) => d !== "");
    else {
      input.certificate = certificate;
      input.private_key = privateKey;
    }
    const req = request(create, 0, input)!;
    busy = true;
    await runOn(project, certName, "Create", req, {
      onanswer: () => {
        busy = false;
        window.location.hash = list;
      },
      onerror: (err) => {
        busy = false;
        errors = formErrors(err, known);
      },
    });
  }
</script>

<FormPage
  title="Add certificate"
  help="certificates"
  submitLabel="Add certificate"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Type" error={errors.fields.type}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={type}>
        <option value="uploaded">Uploaded certificate</option>
        <option value="managed">Managed certificate (HLab CA)</option>
      </select>
    {/snippet}
  </Field>
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  {#if type === "managed"}
    <Field
      label="Domain names"
      error={errors.fields.domain_names}
      hint="One per line; each must lie under a primary DNS zone of this project, for example www.lab.internal or *.lab.internal."
    >
      {#snippet children(c)}
        <textarea id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} rows="3" spellcheck="false" bind:value={domains}
        ></textarea>
      {/snippet}
    </Field>
  {:else}
    <Field label="Certificate" error={errors.fields.certificate} hint="PEM, the certificate first, then its chain.">
      {#snippet children(c)}
        <textarea id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} rows="6" spellcheck="false" bind:value={certificate}
        ></textarea>
      {/snippet}
    </Field>
    <Field label="Private key" error={errors.fields.private_key} hint="PEM; it is never shown again.">
      {#snippet children(c)}
        <textarea id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} rows="6" spellcheck="false" bind:value={privateKey}
        ></textarea>
      {/snippet}
    </Field>
  {/if}
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  select,
  input,
  textarea {
    width: 100%;
  }
  textarea {
    font-family: var(--mono);
  }
</style>
