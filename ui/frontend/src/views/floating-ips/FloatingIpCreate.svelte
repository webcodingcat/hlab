<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, listAll } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/floatingIps";
  import { showError } from "../../lib/toast";

  // The create form of a floating IP: home location or a server to assign
  // it to at once (then the server's location applies), protocol, name and
  // labels. The list opens as soon as the API answers; the toast follows
  // the request.
  let { project, list }: { project: string; list: string } = $props();

  type Location = { id: number; name: string; city: string };
  type Server = { id: number; name: string; location: { name: string } };

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
      showError("Load the floating IP options", err);
    }
  }

  $effect(() => {
    load();
  });

  let location = $state("");
  let server = $state("");
  let type = $state("ipv4");
  let name = $state("");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["home_location", "server", "type", "name", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if ("error" in l) return;
    const fipName = name.trim();
    const input: Input = { type, labels: l.labels };
    if (fipName) input.name = fipName;
    if (server) input.server = Number(server);
    else input.home_location = location;
    const req = request(create, 0, input)!;
    busy = true;
    await runAction(
      actionTitle(resourceType(req.path), fipName, "Create"),
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
  title="Create floating IP"
  help="floating-ips"
  submitLabel="Create floating IP"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  {#if !server}
    <Field label="Location" error={errors.fields.home_location} hint="The home location; the IP is assignable to servers in its network zone.">
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
          {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
        </select>
      {/snippet}
    </Field>
  {/if}
  <Field label="Server" error={errors.fields.server} hint="Optional. The IP is assigned at once; the server's location becomes its home.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={server}>
        <option value="">No server</option>
        {#each servers as s (s.id)}<option value={`${s.id}`}>{s.name} ({s.location.name})</option>{/each}
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
  <Field label="Name" error={errors.fields.name} hint="Optional; unique in the project.">
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  select,
  input {
    width: 100%;
  }
</style>
