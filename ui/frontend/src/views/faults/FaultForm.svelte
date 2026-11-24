<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import { hetznerListAll, listAll } from "../../lib/api";
  import { mgmt, service } from "../../lib/bridge";
  import {
    defaultForm,
    faultKinds,
    faultResources,
    formField,
    kindLabel,
    requestBody,
    showsSeed,
    type FaultForm,
    type FaultKind,
  } from "../../lib/faults";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { showError } from "../../lib/toast";

  // The create form of a fault: the kind with its explanation, then only
  // the fields of that kind, the seed where the kind takes one and the
  // duration. Target selects list only the resources that fit the kind;
  // with none, the select says so and Create is disabled. The list opens
  // again after the create.
  let { project, list }: { project: string; list: string } = $props();

  type Named = { id: number; name: string };
  type Volume = Named & { server: number | null };
  type Location = { name: string; city: string };
  type CodeStatus = { code: string; status: number };

  let servers = $state<Named[]>([]);
  let volumes = $state<Volume[]>([]);
  let boxes = $state<Named[]>([]);
  let locations = $state<Location[]>([]);
  let codes = $state<CodeStatus[]>([]);
  let loaded = $state(false);

  const byName = (a: Named, b: Named) => a.name.localeCompare(b.name);

  async function load() {
    try {
      const [s, v, b, l, c] = await Promise.all([
        listAll<Named>(project, "/servers", "servers"),
        listAll<Volume>(project, "/volumes", "volumes"),
        hetznerListAll<Named>(project, "/storage_boxes", "storage_boxes"),
        service<Location[]>("Service", "Locations"),
        service<CodeStatus[]>("Service", "ErrorCodes"),
      ]);
      servers = s.sort(byName);
      volumes = v.sort(byName);
      boxes = b.sort(byName);
      locations = l;
      codes = c;
      loaded = true;
    } catch (err) {
      showError("Load the fault options", err);
    }
  }

  $effect(() => {
    load();
  });

  let form = $state<FaultForm>(defaultForm("server_crash"));
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = Object.keys(defaultForm("server_crash"));
  const info = $derived(faultKinds.find((k) => k.kind === form.kind)!);

  function setKind(kind: FaultKind) {
    form = defaultForm(kind);
    errors = { fields: {} };
  }

  function setSegment(key: "targetMode" | "only" | "traffic", value: string) {
    (form as Record<typeof key, string>)[key] = value;
  }

  type Option = { value: string; text: string };
  type Group = { label?: string; options: Option[] };

  const serverOptions = $derived(servers.map((s) => ({ value: `server:${s.id}`, text: s.name })));

  // The target select's groups and the text when nothing fits; none for
  // kinds without a resource target and for latency between locations.
  const targetSelect = $derived.by((): { groups: Group[]; none: string } | undefined => {
    switch (form.kind) {
      case "server_crash":
      case "server_freeze":
      case "network":
        return { groups: [{ options: serverOptions }], none: "No servers in this project" };
      case "latency":
        if (form.targetMode === "locations") return undefined;
        return { groups: [{ options: serverOptions }], none: "No servers in this project" };
      case "volume_unavailable":
        return {
          groups: [
            {
              options: volumes.filter((v) => v.server !== null).map((v) => ({ value: `volume:${v.id}`, text: v.name })),
            },
          ],
          none: "No attached volumes in this project",
        };
      case "disk_slow":
        return {
          groups: [
            { label: "Servers", options: serverOptions },
            { label: "Volumes", options: volumes.map((v) => ({ value: `volume:${v.id}`, text: v.name })) },
          ].filter((g) => g.options.length > 0),
          none: "No servers or volumes in this project",
        };
      case "storage_box_unavailable":
        return {
          groups: [{ options: boxes.map((b) => ({ value: `storage_box:${b.id}`, text: b.name })) }],
          none: "No storage boxes in this project",
        };
      default:
        return undefined;
    }
  });

  const nothingFits = $derived(
    loaded &&
      ((targetSelect !== undefined && targetSelect.groups.every((g) => g.options.length === 0)) ||
        (form.kind === "latency" && form.targetMode === "locations" && locations.length === 0)),
  );

  // fieldErrors maps the management API's field names ("params.percent")
  // to the form's fields before the errors go under them.
  function fieldErrors(err: unknown): FormErrors {
    const e = err as { code?: unknown; message?: unknown; fields?: Record<string, string[]> } | null;
    if (!e || typeof e !== "object" || !e.fields) return formErrors(err, known);
    const fields: Record<string, string[]> = {};
    for (const [name, messages] of Object.entries(e.fields)) {
      const f = formField(name);
      fields[f] = [...(fields[f] ?? []), ...messages];
    }
    return formErrors({ code: e.code, message: e.message, fields }, known);
  }

  async function submit() {
    errors = { fields: {} };
    busy = true;
    try {
      await mgmt("POST", `/projects/${encodeURIComponent(project)}/faults`, requestBody(form));
      window.location.hash = list;
    } catch (err) {
      errors = fieldErrors(err);
    } finally {
      busy = false;
    }
  }

  const durations: { value: FaultForm["duration"]; text: string }[] = [
    { value: "", text: "Until ended" },
    { value: "1m", text: "1 minute" },
    { value: "5m", text: "5 minutes" },
    { value: "15m", text: "15 minutes" },
    { value: "60m", text: "60 minutes" },
    { value: "custom", text: "Custom" },
  ];

  const segmentId = $props.id();
</script>

{#snippet segmented(label: string, key: "targetMode" | "only" | "traffic", options: { value: string; text: string }[])}
  <div class="group">
    <span class="legend" id={`${segmentId}-${key}`}>{label}</span>
    <div class="segmented" role="radiogroup" aria-labelledby={`${segmentId}-${key}`}>
      {#each options as o (o.value)}
        <label class:on={form[key] === o.value}>
          <input
            type="radio"
            name={key}
            value={o.value}
            checked={form[key] === o.value}
            onchange={() => setSegment(key, o.value)}
          />{o.text}
        </label>
      {/each}
    </div>
    {#if errors.fields[key]}<div class="error">{errors.fields[key]}</div>{/if}
  </div>
{/snippet}

{#snippet numberField(label: string, key: "percent" | "perHour" | "lossPercent" | "delayMs" | "jitterMs" | "iops" | "seed", hint?: string)}
  <Field {label} error={errors.fields[key]} {hint}>
    {#snippet children(c)}
      <input
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        inputmode="numeric"
        autocomplete="off"
        bind:value={form[key]}
      />
    {/snippet}
  </Field>
{/snippet}

{#snippet locationField(label: string, key: "from" | "to")}
  <Field {label} error={errors.fields[key]}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={form[key]}>
        {#if nothingFits}
          <option value="" disabled>No locations in the catalog</option>
        {:else}
          <option value="" disabled>Select a location</option>
          {#each locations as l (l.name)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
        {/if}
      </select>
    {/snippet}
  </Field>
{/snippet}

<FormPage
  title="Create fault"
  help="faults"
  submitLabel="Create fault"
  {busy}
  disabled={!loaded || nothingFits}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Kind" error={errors.fields.kind} hint={info.explanation}>
    {#snippet children(c)}
      <select
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        value={form.kind}
        onchange={(e) => setKind((e.currentTarget as HTMLSelectElement).value as FaultKind)}
      >
        {#each faultKinds as k (k.kind)}<option value={k.kind}>{kindLabel(k.kind)}</option>{/each}
      </select>
    {/snippet}
  </Field>

  {#if form.kind === "latency"}
    {@render segmented("Latency on", "targetMode", [
      { value: "server", text: "Server" },
      { value: "locations", text: "Between locations" },
    ])}
  {/if}

  {#if targetSelect}
    <Field label="Target" error={errors.fields.target}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={form.target}>
          {#if nothingFits}
            <option value="" disabled>{targetSelect.none}</option>
          {:else}
            <option value="" disabled>Select a target</option>
            {#each targetSelect.groups as g, i (i)}
              {#if g.label}
                <optgroup label={g.label}>
                  {#each g.options as o (o.value)}<option value={o.value}>{o.text}</option>{/each}
                </optgroup>
              {:else}
                {#each g.options as o (o.value)}<option value={o.value}>{o.text}</option>{/each}
              {/if}
            {/each}
          {/if}
        </select>
      {/snippet}
    </Field>
  {/if}

  {#if form.kind === "latency" && form.targetMode === "locations"}
    {@render locationField("From", "from")}
    {@render locationField("To", "to")}
  {/if}

  {#if form.kind === "api_error"}
    <Field label="Error code" error={errors.fields.code}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={form.code}>
          {#each codes as k (k.code)}<option value={k.code}>{k.code} ({k.status})</option>{/each}
        </select>
      {/snippet}
    </Field>
    {@render numberField("Percent", "percent", "Share of the requests that fail, 1 to 100.")}
    {@render segmented("Only", "only", [
      { value: "read", text: "Reads" },
      { value: "write", text: "Writes" },
      { value: "", text: "All" },
    ])}
    <Field label="Resource type" error={errors.fields.resource}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={form.resource}>
          <option value="">All</option>
          {#each faultResources as r (r)}<option value={r}>{r}</option>{/each}
        </select>
      {/snippet}
    </Field>
  {:else if form.kind === "rate_limit"}
    {@render numberField("Requests per hour", "perHour", "1 to 3600.")}
  {:else if form.kind === "network"}
    {@render segmented("Traffic", "traffic", [
      { value: "public", text: "Public" },
      { value: "private", text: "Private" },
      { value: "all", text: "All" },
    ])}
    {@render numberField("Loss percent", "lossPercent", "1 to 100; 100 cuts the network off.")}
  {:else if form.kind === "latency"}
    {@render numberField("Delay (ms)", "delayMs", "1 to 10000.")}
    {@render numberField("Jitter (ms)", "jitterMs", "0 to the delay.")}
  {:else if form.kind === "disk_slow"}
    {@render numberField("IOPS", "iops", "10 to 1000000, counted in I/Os of 8 KiB.")}
  {/if}

  {#if showsSeed(form)}
    {@render numberField("Seed", "seed", "Empty: HLab chooses one and shows it.")}
  {/if}

  <Field label="Duration" error={form.duration === "custom" ? undefined : errors.fields.duration}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={form.duration}>
        {#each durations as d (d.value)}<option value={d.value}>{d.text}</option>{/each}
      </select>
    {/snippet}
  </Field>
  {#if form.duration === "custom"}
    <Field label="Custom duration" error={errors.fields.duration} hint="1 s to 24 h, for example 90s, 10m or 2h.">
      {#snippet children(c)}
        <input
          id={c.id}
          aria-describedby={c.describedby}
          aria-invalid={c.invalid}
          autocomplete="off"
          bind:value={form.customDuration}
        />
      {/snippet}
    </Field>
  {/if}
</FormPage>

<style>
  .group {
    display: flex;
    flex-direction: column;
    gap: var(--s1);
    margin-bottom: var(--s4);
  }
  .legend {
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
