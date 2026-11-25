<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/primaryIps";
  import { showError } from "../../lib/toast";
  import { assignable, type Server } from "./Section.svelte";

  // The create form of a primary IP: name, location, protocol, an optional
  // powered-off server of that location to assign it to at once, auto
  // delete and labels. The list opens as soon as the API answers; the toast
  // follows the request.
  let { project, list }: { project: string; list: string } = $props();

  type Location = { id: number; name: string; city: string };

  let locations = $state<Location[]>([]);
  let servers = $state<Server[]>([]);

  async function load() {
    try {
      const [l, s] = await Promise.all([
        listAll<Location>(project, "/locations", "locations"),
        listAll<Server>(project, "/servers", "servers"),
      ]);
      locations = l.sort((a, b) => a.name.localeCompare(b.name));
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
      if (!location && locations.length > 0) location = locations[0].name;
    } catch (err) {
      showError("Load the primary IP options", err);
    }
  }

  $effect(() => {
    load();
  });

  let name = $state("");
  let location = $state("");
  let type = $state("ipv4");
  let server = $state("");
  let autoDelete = $state(false);
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const candidates = $derived(assignable(servers, location, type));

  // A server chosen before the location or protocol changed may no longer fit.
  $effect(() => {
    if (server && !candidates.some((s) => `${s.id}` === server)) server = "";
  });

  const known = ["name", "location", "type", "assignee_id", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const pipName = name.trim();
    const input: Input = { name: pipName, type, location, auto_delete: autoDelete, labels: l.labels };
    if (server) input.assignee_id = Number(server);
    const req = request(create, 0, input)!;
    busy = true;
    await runOn(project, pipName, "Create", req, {
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
  title="Create primary IP"
  help="primary-ips"
  submitLabel="Create primary IP"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Name" error={errors.fields.name} hint="Unique in the project.">
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <Field label="Location" error={errors.fields.location} hint="The IP is assignable only to servers in this location.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
        {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
      </select>
    {/snippet}
  </Field>
  <Field label="Protocol" error={errors.fields.type}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={type}>
        <option value="ipv4">IPv4</option>
        <option value="ipv6">IPv6</option>
      </select>
    {/snippet}
  </Field>
  <Field
    label="Server"
    error={errors.fields.assignee_id}
    hint="Optional. Powered-off servers of the location without a public IP of this protocol; the IP is assigned at once."
  >
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={server}>
        <option value="">No server</option>
        {#each candidates as s (s.id)}<option value={`${s.id}`}>{s.name}</option>{/each}
      </select>
    {/snippet}
  </Field>
  <label class="check"><input type="checkbox" bind:checked={autoDelete} />Auto delete: delete the IP together with its server</label>
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .check {
    display: flex;
    margin-bottom: var(--s4);
    align-items: center;
    gap: var(--s2);
  }
</style>
