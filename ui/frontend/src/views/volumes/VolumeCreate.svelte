<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, listAll } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/volumes";
  import { showError } from "../../lib/toast";

  // The create form of a volume: size, name, location, an optional server
  // with mount option and filesystem, labels. With a server the location
  // field disappears and the server's location applies. The list opens as
  // soon as the API answers; the toast follows the create action.
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
      showError("Load the volume options", err);
    }
  }

  $effect(() => {
    load();
  });

  let size = $state(10);
  let name = $state("");
  let location = $state("");
  let server = $state("");
  let automount = $state(true);
  let format = $state("ext4");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["size", "name", "location", "server", "automount", "format", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if (!Number.isInteger(size) || size < 10 || size > 10240) {
      errors.fields.size = "Enter a size from 10 to 10240 GB.";
    }
    if ("error" in l || Object.keys(errors.fields).length > 0) return;
    const volumeName = name.trim();
    const input: Input = { name: volumeName, size, labels: l.labels };
    if (server) Object.assign(input, { server: Number(server), automount, format });
    else input.location = location;
    const req = request(create, 0, input)!;
    busy = true;
    await runAction(
      `Create volume ${volumeName}`,
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
  title="Create volume"
  help="volumes"
  submitLabel="Create volume"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Size" error={errors.fields.size} hint="10 to 10240 GB. A volume can grow later but not shrink.">
    {#snippet children(c)}
      <input
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        type="number"
        min="10"
        max="10240"
        step="1"
        bind:value={size}
      />
    {/snippet}
  </Field>
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  {#if !server}
    <Field label="Location" error={errors.fields.location}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
          {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
        </select>
      {/snippet}
    </Field>
  {/if}
  <Field label="Server" error={errors.fields.server} hint="Optional. The volume is created in the server's location.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={server}>
        <option value="">No server</option>
        {#each servers as s (s.id)}<option value={`${s.id}`}>{s.name} ({s.location.name})</option>{/each}
      </select>
    {/snippet}
  </Field>
  {#if server}
    <fieldset>
      <legend>Mount</legend>
      <label class="check"><input type="radio" name="mount" value={true} bind:group={automount} />Automatic</label>
      <label class="check"><input type="radio" name="mount" value={false} bind:group={automount} />Manual</label>
      {#if errors.fields.automount}<div class="error">{errors.fields.automount}</div>{/if}
    </fieldset>
    <fieldset>
      <legend>Filesystem</legend>
      <label class="check"><input type="radio" name="format" value="ext4" bind:group={format} />ext4</label>
      <label class="check"><input type="radio" name="format" value="xfs" bind:group={format} />xfs</label>
      {#if errors.fields.format}<div class="error">{errors.fields.format}</div>{/if}
    </fieldset>
  {/if}
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
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
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  select,
  input:not([type="radio"]) {
    width: 100%;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
