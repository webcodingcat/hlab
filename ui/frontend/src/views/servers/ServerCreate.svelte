<script lang="ts">
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import SecretDialog from "../../lib/SecretDialog.svelte";
  import { runAction, type Action, type ActionResult } from "../../lib/actions";
  import { api, listAll, runApi } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, tabActions } from "../../lib/serverActions";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import { imageGroups, imageLabel, typeLabel, typesIn, usableImages, type Image, type Named, type ServerType } from "./servers";

  // The create form of a server in the order of the Hetzner Console. The
  // request runs under an action toast; once it answers, a root password
  // shows once in the secret dialog and the new server's detail page opens.
  let { project }: { project: string } = $props();

  type Location = Named & { city: string };
  type PrimaryIp = Named & { ip: string; type: "ipv4" | "ipv6"; location: { name: string }; assignee_id: number | null };
  type Volume = Named & { size: number; location: { name: string }; server: number | null };
  type Created = ActionResult & { server: { id: number }; root_password: string | null };

  let locations = $state<Location[]>([]);
  let images = $state<Image[]>([]);
  let types = $state<ServerType[]>([]);
  let sshKeys = $state<Named[]>([]);
  let networks = $state<Named[]>([]);
  let primaryIps = $state<PrimaryIp[]>([]);
  let volumes = $state<Volume[]>([]);
  let firewalls = $state<Named[]>([]);
  let placementGroups = $state<Named[]>([]);
  let serverNames = $state<string[]>([]);

  async function load() {
    try {
      const all = <T,>(path: string, key: string) => listAll<T>(project, path, key);
      const [l, i, t, k, n, p, v, f, g, s] = await Promise.all([
        all<Location>("/locations", "locations"),
        all<Image>("/images", "images"),
        all<ServerType>("/server_types", "server_types"),
        all<Named>("/ssh_keys", "ssh_keys"),
        all<Named>("/networks", "networks"),
        all<PrimaryIp>("/primary_ips", "primary_ips"),
        all<Volume>("/volumes", "volumes"),
        all<Named>("/firewalls", "firewalls"),
        all<Named>("/placement_groups", "placement_groups"),
        all<Named>("/servers", "servers"),
      ]);
      locations = l.sort((a, b) => a.name.localeCompare(b.name));
      images = usableImages(i);
      types = t;
      [sshKeys, networks, primaryIps, volumes, firewalls, placementGroups] = [k, n, p, v, f, g];
      serverNames = s.map((x) => x.name);
      if (!location && locations.length > 0) location = locations[0].name;
    } catch (err) {
      showError("Load the server options", err);
    }
  }

  $effect(() => {
    load();
  });

  // Form values.
  let location = $state("");
  let image = $state("");
  let serverType = $state("");
  let ipv4 = $state(true);
  let ipv6 = $state(true);
  let primaryIpv4 = $state("");
  let primaryIpv6 = $state("");
  let chosenNetworks = $state<number[]>([]);
  let chosenKeys = $state<number[]>([]);
  let chosenVolumes = $state<number[]>([]);
  let automount = $state(true);
  let chosenFirewalls = $state<number[]>([]);
  let backups = $state(false);
  let placementGroup = $state("");
  let labelRows = $state<LabelRow[]>([]);
  let userData = $state("");
  let customName = $state<string | undefined>();

  const groups = $derived(imageGroups(images));

  // Only the types available in the chosen location.
  const typeOptions = $derived(typesIn(types, location));
  $effect(() => {
    if (!typeOptions.some((t) => t.name === serverType)) serverType = typeOptions[0]?.name ?? "";
  });

  const freeIps = (type: "ipv4" | "ipv6") =>
    primaryIps.filter((p) => p.type === type && p.assignee_id === null && p.location.name === location);
  const ipv4Options = $derived(freeIps("ipv4"));
  const ipv6Options = $derived(freeIps("ipv6"));
  const volumeOptions = $derived(
    volumes.filter((v) => v.location.name === location && v.server === null),
  );

  // The default name <type>-<location>-<n> with the first n not taken.
  const defaultName = $derived.by(() => {
    if (!serverType || !location) return "";
    let n = 1;
    while (serverNames.includes(`${serverType}-${location}-${n}`)) n++;
    return `${serverType}-${location}-${n}`;
  });
  const name = $derived(customName ?? defaultName);

  // Submit.
  const known = [
    "name",
    "location",
    "server_type",
    "image",
    "public_net",
    "networks",
    "ssh_keys",
    "volumes",
    "automount",
    "firewalls",
    "placement_group",
    "labels",
    "user_data",
  ];
  let errors = $state<FormErrors>({ fields: {} });
  let busy = $state(false);
  let password = $state<string | undefined>();
  let secretOpen = $state(false);
  let createdId = 0;

  const listHref = $derived(href({ area: "project", project, view: "servers", page: { kind: "list" } }));

  function openDetail() {
    password = undefined;
    go(href({ area: "project", project, view: "servers", page: { kind: "detail", id: createdId, tab: "overview" } }));
  }

  function go(h: string) {
    window.location.hash = h;
  }

  function closeSecret(open: boolean) {
    secretOpen = open;
    if (!open) openDetail();
  }

  function body(labels: Record<string, string>) {
    const b: Record<string, unknown> = {
      name: name.trim(),
      location,
      server_type: serverType,
      image,
      public_net: {
        enable_ipv4: ipv4,
        enable_ipv6: ipv6,
        ...(ipv4 && ipv4Options.some((p) => `${p.id}` === primaryIpv4) ? { ipv4: Number(primaryIpv4) } : {}),
        ...(ipv6 && ipv6Options.some((p) => `${p.id}` === primaryIpv6) ? { ipv6: Number(primaryIpv6) } : {}),
      },
      labels,
    };
    const vols = chosenVolumes.filter((id) => volumeOptions.some((v) => v.id === id));
    if (chosenNetworks.length > 0) b.networks = chosenNetworks;
    if (chosenKeys.length > 0) b.ssh_keys = chosenKeys.map(String);
    if (vols.length > 0) Object.assign(b, { volumes: vols, automount });
    if (chosenFirewalls.length > 0) b.firewalls = chosenFirewalls.map((firewall) => ({ firewall }));
    if (placementGroup) b.placement_group = Number(placementGroup);
    if (userData.trim()) b.user_data = userData;
    return b;
  }

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if (!image) errors.fields.image = "Choose an image.";
    if (!serverType) errors.fields.server_type = "Choose a type available in this location.";
    if ("error" in l || Object.keys(errors.fields).length > 0) return;
    busy = true;
    const withBackups = backups;
    // The answer opens the password or the detail page at once; the toast
    // follows the create action and the start after it.
    const result = await runAction(
      `Create server ${name.trim()}`,
      async () => {
        try {
          const res = await api<Created>(project, "POST", "/servers", body(l.labels));
          createdId = res.server.id;
          if (res.root_password) {
            password = res.root_password;
            secretOpen = true;
          } else {
            openDetail();
          }
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
    const req = request(tabActions.enable_backup, createdId);
    if (result && withBackups && req) {
      await runApi(project, `Enable backups ${name.trim()}`, req.method, req.path, req.body);
    }
  }
</script>

<FormPage
  title="Create server"
  help="servers"
  submitLabel="Create server"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => go(listHref)}
>
  <Field label="Location" error={errors.fields.location}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
        {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
      </select>
    {/snippet}
  </Field>

  <Field label="Image" error={errors.fields.image}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={image}>
        <option value="" disabled>Choose an image</option>
        {#each groups as g (g.label)}
          {#if g.images.length > 0}
            <optgroup label={g.label}>
              {#each g.images as i (i.id)}<option value={`${i.id}`}>{imageLabel(i)}</option>{/each}
            </optgroup>
          {/if}
        {/each}
      </select>
    {/snippet}
  </Field>

  <Field label="Type" error={errors.fields.server_type}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={serverType}>
        {#each typeOptions as t (t.id)}
          <option value={t.name}>{typeLabel(t)}</option>
        {/each}
      </select>
    {/snippet}
  </Field>

  <fieldset>
    <legend>Networking</legend>
    <label class="check"><input type="checkbox" role="switch" aria-checked={ipv4} bind:checked={ipv4} />Public IPv4</label>
    {#if ipv4 && ipv4Options.length > 0}
      <Field label="Primary IPv4">
        {#snippet children(c)}
          <select id={c.id} bind:value={primaryIpv4}>
            <option value="">New primary IP</option>
            {#each ipv4Options as p (p.id)}<option value={`${p.id}`}>{p.name} ({p.ip})</option>{/each}
          </select>
        {/snippet}
      </Field>
    {/if}
    <label class="check"><input type="checkbox" role="switch" aria-checked={ipv6} bind:checked={ipv6} />Public IPv6</label>
    {#if ipv6 && ipv6Options.length > 0}
      <Field label="Primary IPv6">
        {#snippet children(c)}
          <select id={c.id} bind:value={primaryIpv6}>
            <option value="">New primary IP</option>
            {#each ipv6Options as p (p.id)}<option value={`${p.id}`}>{p.name} ({p.ip})</option>{/each}
          </select>
        {/snippet}
      </Field>
    {/if}
    {#if errors.fields.public_net}<div class="error">{errors.fields.public_net}</div>{/if}
    <div class="sub">Private networks</div>
    {#each networks as n (n.id)}
      <label class="check"><input type="checkbox" value={n.id} bind:group={chosenNetworks} />{n.name}</label>
    {:else}<p class="muted">This project has no networks.</p>{/each}
    {#if errors.fields.networks}<div class="error">{errors.fields.networks}</div>{/if}
  </fieldset>

  <fieldset>
    <legend>SSH keys</legend>
    {#each sshKeys as k (k.id)}
      <label class="check"><input type="checkbox" value={k.id} bind:group={chosenKeys} />{k.name}</label>
    {:else}<p class="muted">No SSH keys: the root password shows once after the server is created.</p>{/each}
    {#if errors.fields.ssh_keys}<div class="error">{errors.fields.ssh_keys}</div>{/if}
  </fieldset>

  <fieldset>
    <legend>Volumes</legend>
    {#each volumeOptions as v (v.id)}
      <label class="check"><input type="checkbox" value={v.id} bind:group={chosenVolumes} />{v.name} ({v.size} GB)</label>
    {:else}<p class="muted">No free volumes in this location.</p>{/each}
    {#if volumeOptions.length > 0}
      <label class="check"><input type="checkbox" role="switch" aria-checked={automount} bind:checked={automount} />Mount volumes automatically</label>
    {/if}
    {#if errors.fields.volumes || errors.fields.automount}
      <div class="error">{[errors.fields.volumes, errors.fields.automount].filter(Boolean).join(" ")}</div>
    {/if}
  </fieldset>

  <fieldset>
    <legend>Firewalls</legend>
    {#each firewalls as f (f.id)}
      <label class="check"><input type="checkbox" value={f.id} bind:group={chosenFirewalls} />{f.name}</label>
    {:else}<p class="muted">This project has no firewalls.</p>{/each}
    {#if errors.fields.firewalls}<div class="error">{errors.fields.firewalls}</div>{/if}
  </fieldset>

  <fieldset>
    <legend>Backups</legend>
    <label class="check"><input type="checkbox" role="switch" aria-checked={backups} bind:checked={backups} />Enable backups</label>
  </fieldset>

  <Field label="Placement group" error={errors.fields.placement_group}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={placementGroup}>
        <option value="">None</option>
        {#each placementGroups as g (g.id)}<option value={`${g.id}`}>{g.name}</option>{/each}
      </select>
    {/snippet}
  </Field>

  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />

  <Field label="Cloud config" error={errors.fields.user_data}>
    {#snippet children(c)}
      <textarea
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        rows="5"
        placeholder="#cloud-config"
        bind:value={userData}
      ></textarea>
    {/snippet}
  </Field>

  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input
        id={c.id}
        aria-describedby={c.describedby}
        aria-invalid={c.invalid}
        autocomplete="off"
        value={name}
        oninput={(e) => (customName = e.currentTarget.value)}
      />
    {/snippet}
  </Field>
</FormPage>

<SecretDialog bind:open={() => secretOpen, closeSecret} title="Root password" label="password" value={password} />

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
  .sub {
    margin-top: var(--s2);
    color: var(--muted);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  select,
  textarea,
  input:not([type="checkbox"]) {
    width: 100%;
  }
  textarea {
    font-family: var(--mono);
  }
  .muted {
    color: var(--muted);
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
