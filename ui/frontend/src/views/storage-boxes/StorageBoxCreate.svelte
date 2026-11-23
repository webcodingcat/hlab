<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, hetznerApi, hetznerListAll, listAll } from "../../lib/api";
  import { actionTitle, resourceType } from "../../lib/format";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create, typesIn, type BoxType } from "../../lib/resources/storageBoxes";
  import { showError } from "../../lib/toast";

  // The create form of a storage box: location, type (only those offered
  // there), name, password, SSH keys of the project (their public keys are
  // sent), access settings and labels. The list opens as soon as the API
  // answers; the toast follows the create action.
  let { project, list }: { project: string; list: string } = $props();

  type Location = { id: number; name: string; city: string };
  type Key = { id: number; name: string; public_key: string };
  type Type = BoxType & { description?: string };

  let locations = $state<Location[]>([]);
  let types = $state<Type[]>([]);
  let keys = $state<Key[]>([]);

  async function load() {
    try {
      const [l, t, k] = await Promise.all([
        listAll<Location>(project, "/locations", "locations"),
        hetznerListAll<Type>(project, "/storage_box_types", "storage_box_types"),
        listAll<Key>(project, "/ssh_keys", "ssh_keys"),
      ]);
      types = t;
      // Only the locations where at least one type is offered.
      locations = l.filter((x) => typesIn(t, x.name).length > 0).sort((a, b) => a.name.localeCompare(b.name));
      keys = k.sort((a, b) => a.name.localeCompare(b.name));
      if (!location && locations.length > 0) location = locations[0].name;
    } catch (err) {
      showError("Load the storage box options", err);
    }
  }

  $effect(() => {
    load();
  });

  let location = $state("");
  let boxType = $state("");
  let name = $state("");
  let password = $state("");
  let chosenKeys = $state<number[]>([]);
  let access = $state({
    ssh_enabled: false,
    samba_enabled: false,
    webdav_enabled: false,
    zfs_enabled: false,
    reachable_externally: false,
  });
  let labelRows = $state<LabelRow[]>([]);
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const offered = $derived(typesIn(types, location) as Type[]);

  // A type the new location does not offer gives way to the first one there.
  $effect(() => {
    if (!offered.some((t) => t.name === boxType)) boxType = offered[0]?.name ?? "";
  });

  const accessFlags: { key: keyof typeof access; label: string }[] = [
    { key: "ssh_enabled", label: "SSH enabled" },
    { key: "samba_enabled", label: "Samba enabled" },
    { key: "webdav_enabled", label: "WebDAV enabled" },
    { key: "zfs_enabled", label: "ZFS enabled" },
    { key: "reachable_externally", label: "Reachable externally" },
  ];

  const known = ["name", "location", "storage_box_type", "password", "ssh_keys", "access_settings", "labels"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if (!location) errors.fields.location = "Select a location.";
    if (!boxType) errors.fields.storage_box_type = "Select a type.";
    if ("error" in l || Object.keys(errors.fields).length > 0) return;
    const boxName = name.trim();
    const input: Input = {
      name: boxName,
      location,
      storage_box_type: boxType,
      password,
      ssh_keys: keys.filter((k) => chosenKeys.includes(k.id)).map((k) => k.public_key),
      access_settings: { ...access },
      labels: l.labels,
    };
    const req = request(create, 0, input)!;
    busy = true;
    await runAction(
      actionTitle(resourceType(req.path), boxName, "Create"),
      async () => {
        try {
          const res = await hetznerApi<ActionResult>(project, req.method, req.path, req.body);
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
  title="Create Storage Box"
  help="storage-boxes"
  submitLabel="Create Storage Box"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Location" error={errors.fields.location}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
        {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
      </select>
    {/snippet}
  </Field>
  <Field label="Type" error={errors.fields.storage_box_type} hint="Only the types offered in the location are listed.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={boxType}>
        {#each offered as t (t.id)}
          <option value={t.name}>{t.description ? `${t.name} (${t.description})` : t.name}</option>
        {/each}
      </select>
    {/snippet}
  </Field>
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
  <Field label="Password" error={errors.fields.password}>
    {#snippet children(c)}
      <input
        id={c.id}
        type="password"
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        autocomplete="new-password"
        bind:value={password}
      />
    {/snippet}
  </Field>
  <fieldset>
    <legend>SSH keys</legend>
    {#each keys as k (k.id)}
      <label class="check"><input type="checkbox" value={k.id} bind:group={chosenKeys} />{k.name}</label>
    {:else}<p class="muted">This project has no SSH keys.</p>{/each}
    {#if errors.fields.ssh_keys}<div class="error">{errors.fields.ssh_keys}</div>{/if}
  </fieldset>
  <fieldset>
    <legend>Access settings</legend>
    {#each accessFlags as f (f.key)}
      <label class="check"><input type="checkbox" bind:checked={access[f.key]} />{f.label}</label>
    {/each}
    {#if errors.fields.access_settings}<div class="error">{errors.fields.access_settings}</div>{/if}
  </fieldset>
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
  input:not([type="checkbox"]) {
    width: 100%;
  }
  .muted {
    margin: 0;
    color: var(--muted);
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
