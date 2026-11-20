<script lang="ts">
  import { HardDrive, Minus, Plus } from "@lucide/svelte";
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

  // The create form of a volume like the Hetzner Console's volume dialog:
  // size as field with stepper and slider on one value, name prefilled with
  // the first free volume-N, a required server whose location applies,
  // mount option and filesystem, labels. The list opens as soon as the API
  // answers; the toast follows the create action.
  let { project, list }: { project: string; list: string } = $props();

  const minSize = 10;
  const maxSize = 10240;

  type Server = { id: number; name: string; location: { name: string } };

  let servers = $state<Server[]>([]);

  async function load() {
    try {
      const [s, v] = await Promise.all([
        listAll<Server>(project, "/servers", "servers"),
        listAll<{ name: string }>(project, "/volumes", "volumes"),
      ]);
      servers = s.sort((a, b) => a.name.localeCompare(b.name));
      if (name === "") {
        const taken = new Set(v.map((x) => x.name));
        let n = 1;
        while (taken.has(`volume-${n}`)) n++;
        name = `volume-${n}`;
      }
    } catch (err) {
      showError("Load the volume options", err);
    }
  }

  $effect(() => {
    load();
  });

  let size = $state(minSize);
  let name = $state("");
  let server = $state("");
  let automount = $state(true);
  let format = $state("ext4");
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["size", "name", "server", "automount", "format", "labels"];

  function step(by: number) {
    const n = Number.isFinite(size) ? Math.round(size) + by : minSize;
    size = Math.min(maxSize, Math.max(minSize, n));
  }

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if (!Number.isInteger(size) || size < minSize || size > maxSize) {
      errors.fields.size = "Enter a size from 10 to 10240 GB.";
    }
    if (!server) errors.fields.server = "Select a server.";
    if ("error" in l || Object.keys(errors.fields).length > 0) return;
    const volumeName = name.trim();
    const input: Input = { name: volumeName, size, server: Number(server), automount, format, labels: l.labels };
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
  <div class="size-row">
    <div class="icon" aria-hidden="true"><HardDrive size={40} strokeWidth={1.5} /></div>
    <Field label="Size" error={errors.fields.size} hint="A volume can grow later but not shrink.">
      {#snippet children(c)}
        <div class="stepper">
          <input
            id={c.id}
            class="size"
            aria-describedby={c.describedby}
            aria-invalid={c.invalid}
            type="number"
            min={minSize}
            max={maxSize}
            step="1"
            bind:value={size}
          />
          <div class="steps">
            <button type="button" aria-label="Increase size" onclick={() => step(1)}><Plus size={14} /></button>
            <button type="button" aria-label="Decrease size" onclick={() => step(-1)}><Minus size={14} /></button>
          </div>
          <span class="unit">GB</span>
        </div>
      {/snippet}
    </Field>
  </div>
  <div class="slider">
    <input type="range" aria-label="Size in GB" min={minSize} max={maxSize} step="1" bind:value={size} />
    <div class="ends"><span>10 GB</span><span>10,240 GB</span></div>
  </div>
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <Field label="Server" error={errors.fields.server} hint="The volume is bound to the server's location.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} aria-required="true" bind:value={server}>
        <option value="" disabled>Select a server</option>
        {#each servers as s (s.id)}<option value={`${s.id}`}>{s.name} ({s.location.name})</option>{/each}
      </select>
    {/snippet}
  </Field>
  <fieldset>
    <legend>Mount options</legend>
    <div class="segmented">
      <label class:on={automount}><input type="radio" name="mount" value={true} bind:group={automount} />Automatic</label>
      <label class:on={!automount}><input type="radio" name="mount" value={false} bind:group={automount} />Manual</label>
    </div>
    <div class="hint">
      {automount
        ? "The volume is formatted and mounted on the server automatically."
        : "The volume is formatted and attached; you mount it on the server yourself."}
    </div>
    {#if errors.fields.automount}<div class="error">{errors.fields.automount}</div>{/if}
  </fieldset>
  <fieldset>
    <legend>Filesystem</legend>
    <div class="inline">
      <label class="check"><input type="radio" name="format" value="ext4" bind:group={format} />ext4</label>
      <label class="check"><input type="radio" name="format" value="xfs" bind:group={format} />xfs</label>
    </div>
    {#if errors.fields.format}<div class="error">{errors.fields.format}</div>{/if}
  </fieldset>
  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
</FormPage>

<style>
  .size-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--s4);
  }
  .icon {
    color: var(--accent);
    padding-top: var(--s4);
  }
  .stepper {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .size {
    width: 120px;
    font-size: 24px;
    text-align: right;
  }
  .steps {
    display: flex;
    flex-direction: column;
  }
  .steps button {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 18px;
    padding: 0;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
  }
  .steps button:first-child {
    border-radius: var(--radius) var(--radius) 0 0;
  }
  .steps button:last-child {
    border-top: none;
    border-radius: 0 0 var(--radius) var(--radius);
  }
  .unit {
    font-size: 16px;
    color: var(--muted);
  }
  .slider {
    margin-bottom: var(--s4);
  }
  .slider input {
    width: 100%;
  }
  .ends {
    display: flex;
    justify-content: space-between;
    color: var(--muted);
    font-size: 12px;
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
  .segmented {
    display: inline-flex;
    align-self: flex-start;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .segmented label {
    padding: var(--s1) var(--s4);
    cursor: pointer;
  }
  .segmented label + label {
    border-left: 1px solid var(--border);
  }
  .segmented label.on {
    background: var(--accent);
    color: #fff;
  }
  .segmented input {
    position: absolute;
    opacity: 0;
    width: 1px;
    height: 1px;
  }
  .segmented label:has(input:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .inline {
    display: flex;
    gap: var(--s4);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  select,
  input:not([type="radio"]):not([type="range"]):not(.size) {
    width: 100%;
  }
  .hint {
    color: var(--muted);
    font-size: 12px;
    line-height: 16px;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
