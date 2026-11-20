<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { api } from "../../lib/api";
  import { formErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { showSuccess } from "../../lib/toast";

  // The form adding an SSH key: public key, name and labels. The API's
  // field errors show under their fields; after the add the list opens.
  let { project, list }: { project: string; list: string } = $props();

  let publicKey = $state("");
  let name = $state("");
  let rows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<Record<string, string>>({});
  let general = $state<string | undefined>();

  async function submit() {
    const l = toLabels(rows);
    if ("error" in l) return;
    busy = true;
    errors = {};
    general = undefined;
    const keyName = name.trim();
    try {
      await api(project, "POST", "/ssh_keys", { name: keyName, public_key: publicKey.trim(), labels: l.labels });
      showSuccess("Add SSH key", `SSH key ${keyName} added`);
      location.hash = list;
    } catch (err) {
      const fe = formErrors(err, ["public_key", "name", "labels"]);
      errors = fe.fields;
      general = fe.general;
    } finally {
      busy = false;
    }
  }
</script>

<FormPage
  title="Add SSH key"
  help="ssh-keys"
  submitLabel="Add SSH key"
  {busy}
  {general}
  onsubmit={submit}
  oncancel={() => (location.hash = list)}
>
  <Field label="Public key" error={errors.public_key} hint="The public key in OpenSSH format, e.g. ssh-ed25519 AAAA… user@host.">
    {#snippet children(c)}
      <textarea
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        rows="5"
        spellcheck="false"
        autocomplete="off"
        bind:value={publicKey}
      ></textarea>
    {/snippet}
  </Field>
  <Field label="Name" error={errors.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <LabelsField bind:rows error={errors.labels} />
</FormPage>

<style>
  textarea {
    width: 100%;
    font-family: var(--mono);
    resize: vertical;
  }
  input {
    width: 100%;
  }
</style>
