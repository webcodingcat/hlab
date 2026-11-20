<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/zones";

  // The create form of a DNS zone: domain, type (an empty primary zone, a
  // primary zone from an imported zone file, or a secondary zone with its
  // primary nameservers), the default TTL (not with a zone file, whose $TTL
  // applies) and labels. The list opens as soon as the API answers; the
  // toast follows the request.
  let { project, list }: { project: string; list: string } = $props();

  let name = $state("");
  let kind = $state<"empty" | "import" | "secondary">("empty");
  let zonefile = $state("");
  let nameservers = $state<{ address: string; port: number | null }[]>([{ address: "", port: 53 }]);
  let ttl = $state<number | null>(3600);
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["name", "mode", "ttl", "zonefile", "primary_nameservers", "labels"];

  function addNameserver() {
    nameservers = [...nameservers, { address: "", port: 53 }];
  }

  function removeNameserver(i: number) {
    nameservers = nameservers.filter((_, j) => j !== i);
  }

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const zoneName = name.trim();
    const input: Input = { name: zoneName, mode: kind === "secondary" ? "secondary" : "primary", labels: l.labels };
    if (kind === "import") input.zonefile = zonefile;
    else if (ttl != null) input.ttl = ttl;
    if (kind === "secondary") {
      input.primary_nameservers = nameservers
        .filter((n) => n.address.trim() !== "")
        .map((n) => ({ address: n.address.trim(), port: n.port ?? 53 }));
    }
    const req = request(create, 0, input)!;
    busy = true;
    await runAction(
      actionTitle(resourceType(req.path), zoneName, "Create"),
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
  title="Add zone"
  help="dns"
  submitLabel="Add zone"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field
    label="Domain"
    error={errors.fields.name}
    hint="A registrable domain such as example.com, or one label below internal, test or home.arpa such as lab.internal."
  >
    {#snippet children(c)}
      <input
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        autocomplete="off"
        spellcheck="false"
        bind:value={name}
      />
    {/snippet}
  </Field>
  <Field label="Type" error={errors.fields.mode}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={kind}>
        <option value="empty">Empty zone</option>
        <option value="import">Import zone file</option>
        <option value="secondary">Secondary zone</option>
      </select>
    {/snippet}
  </Field>
  {#if kind === "import"}
    <Field label="Zone file" error={errors.fields.zonefile} hint="The zone file in BIND format; its $TTL is the default TTL.">
      {#snippet children(c)}
        <textarea
          id={c.id}
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          rows="10"
          spellcheck="false"
          bind:value={zonefile}
        ></textarea>
      {/snippet}
    </Field>
  {/if}
  {#if kind === "secondary"}
    <fieldset>
      <legend>Primary nameservers</legend>
      {#each nameservers as _, i (i)}
        <div class="row">
          <input
            aria-label={`Address ${i + 1}`}
            placeholder="198.51.100.53"
            autocomplete="off"
            spellcheck="false"
            bind:value={nameservers[i].address}
          />
          <input class="port" aria-label={`Port ${i + 1}`} type="number" min="1" max="65535" bind:value={nameservers[i].port} />
          <Button variant="icon" label={`Remove nameserver ${i + 1}`} onclick={() => removeNameserver(i)}>
            <X size={16} aria-hidden="true" />
          </Button>
        </div>
      {/each}
      <div>
        <Button onclick={addNameserver}><Plus size={16} aria-hidden="true" />Add nameserver</Button>
      </div>
      {#if errors.fields.primary_nameservers}<div class="error">{errors.fields.primary_nameservers}</div>{/if}
    </fieldset>
  {/if}
  {#if kind !== "import"}
    <Field label="TTL" error={errors.fields.ttl} hint="The default TTL of the zone's records in seconds, at least 60.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} type="number" min="60" bind:value={ttl} />
      {/snippet}
    </Field>
  {/if}
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  input,
  select,
  textarea {
    width: 100%;
  }
  textarea {
    font-family: var(--mono);
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    margin: 0 0 var(--s4);
    padding: 0;
    border: none;
  }
  legend {
    margin-bottom: var(--s1);
    font-weight: 600;
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .row .port {
    width: 7em;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
