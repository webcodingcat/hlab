<script lang="ts">
  import Button from "../lib/Button.svelte";
  import Field from "../lib/Field.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import { mgmt, service } from "../lib/bridge";
  import { action, showError } from "../lib/toast";
  import { running } from "../state";

  // Settings mirrors settings.Settings (json tags equal to the toml keys).
  type Settings = {
    cloud: { stop_behaviour: string };
    network: { public_range: string };
    storage: { data_folder: string };
    resources: { reserve_ram_mb: number; reserve_disk_gb: number };
    certificates: { trust_ca: boolean };
    logging: { level: string };
  };
  type Invalid = { field: string; message: string } | null;

  const nextStart = "Applies at the next cloud start.";

  let form = $state<Settings | undefined>();
  let errors = $state<Record<string, string>>({});
  let saving = $state(false);
  let saved = $state(false);
  let switching = $state(false);

  async function load() {
    try {
      form = await service<Settings>("Service", "Settings");
    } catch (err) {
      showError("Load settings", err);
    }
  }
  load();

  // describedBy points an invalid control at its message only, so the
  // message is what aria-describedby names; otherwise at hint and message.
  function describedBy(c: { id: string; describedby: string | undefined; invalid: boolean }) {
    return c.invalid ? `${c.id}-error` : c.describedby;
  }

  const ipv4Range = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/;

  // check finds what the bridge cannot even decode; the settings package
  // checks the rest.
  function check(s: Settings): Record<string, string> {
    const found: Record<string, string> = {};
    const m = ipv4Range.exec(s.network.public_range.trim());
    if (!m || m.slice(1, 5).some((o) => Number(o) > 255) || Number(m[5]) > 32) {
      found["network.public_range"] = "must be an IPv4 range like 198.18.0.0/16";
    }
    if (!Number.isInteger(s.resources.reserve_ram_mb)) found["resources.reserve_ram_mb"] = "must be a whole number";
    if (!Number.isInteger(s.resources.reserve_disk_gb)) found["resources.reserve_disk_gb"] = "must be a whole number";
    return found;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!form || saving) return;
    errors = check(form);
    if (Object.keys(errors).length > 0) return;
    saving = true;
    saved = false;
    const s = $state.snapshot(form);
    s.network.public_range = s.network.public_range.trim();
    try {
      const invalid = await service<Invalid>("Service", "SaveSettings", s);
      if (invalid) {
        errors = { [invalid.field]: invalid.message };
      } else {
        saved = true;
        await load();
      }
    } catch (err) {
      showError("Save settings", err);
    } finally {
      saving = false;
    }
  }

  // The trust switch goes through the running cloud, which owns trust_ca.
  async function toggleTrust() {
    if (!form || switching) return;
    const on = !form.certificates.trust_ca;
    switching = true;
    try {
      const res = await action(on ? "Trust the hlab CA" : "Stop trusting the hlab CA", () =>
        mgmt<{ on: boolean }>("PUT", "/trust", { on }),
      );
      if (res) form.certificates.trust_ca = res.on;
    } finally {
      switching = false;
    }
  }

  async function saveCA() {
    try {
      const pem = await mgmt<string>("GET", "/ca.pem");
      const url = URL.createObjectURL(new Blob([pem], { type: "application/x-pem-file" }));
      const a = document.createElement("a");
      a.href = url;
      a.download = "hlab-ca.pem";
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      showError("Save CA certificate", err);
    }
  }
</script>

<div class="title-row">
  <h1>Settings</h1>
  <HelpButton view="settings" />
</div>

{#if form}
  <form onsubmit={save} novalidate>
    <fieldset>
      <legend>Cloud</legend>
      <Field
        label="Stop behaviour"
        error={errors["cloud.stop_behaviour"]}
        hint="Applies at the next stop; at a Windows shutdown only after the next cloud start."
      >
        {#snippet children(c)}
          <select id={c.id} aria-describedby={describedBy(c)} aria-invalid={c.invalid} bind:value={form!.cloud.stop_behaviour}>
            <option value="save">Save the VMs' state</option>
            <option value="shutdown">Shut the VMs down</option>
          </select>
        {/snippet}
      </Field>
    </fieldset>

    <fieldset>
      <legend>Network</legend>
      <Field label="Public range" error={errors["network.public_range"]} hint={nextStart}>
        {#snippet children(c)}
          <input
            id={c.id}
            aria-describedby={describedBy(c)}
            aria-invalid={c.invalid}
            autocomplete="off"
            bind:value={form!.network.public_range}
          />
        {/snippet}
      </Field>
    </fieldset>

    <fieldset>
      <legend>Storage</legend>
      <Field label="Data folder" error={errors["storage.data_folder"]} hint={`${nextStart} Existing data is not moved.`}>
        {#snippet children(c)}
          <input
            id={c.id}
            aria-describedby={describedBy(c)}
            aria-invalid={c.invalid}
            autocomplete="off"
            bind:value={form!.storage.data_folder}
          />
        {/snippet}
      </Field>
    </fieldset>

    <fieldset>
      <legend>Resources</legend>
      <Field label="Reserved RAM (MB)" error={errors["resources.reserve_ram_mb"]} hint={nextStart}>
        {#snippet children(c)}
          <input
            id={c.id}
            type="number"
            min="0"
            step="1"
            aria-describedby={describedBy(c)}
            aria-invalid={c.invalid}
            bind:value={form!.resources.reserve_ram_mb}
          />
        {/snippet}
      </Field>
      <Field label="Reserved disk (GB)" error={errors["resources.reserve_disk_gb"]} hint={nextStart}>
        {#snippet children(c)}
          <input
            id={c.id}
            type="number"
            min="0"
            step="1"
            aria-describedby={describedBy(c)}
            aria-invalid={c.invalid}
            bind:value={form!.resources.reserve_disk_gb}
          />
        {/snippet}
      </Field>
    </fieldset>

    <fieldset>
      <legend>Logging</legend>
      <Field label="Log level" error={errors["logging.level"]} hint="Applies at once while the cloud runs.">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={describedBy(c)} aria-invalid={c.invalid} bind:value={form!.logging.level}>
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        {/snippet}
      </Field>
    </fieldset>

    <div class="actions">
      <span role="status" class="small muted">{saved ? "Saved." : ""}</span>
      <Button type="submit" variant="primary" loading={saving}>Save settings</Button>
    </div>
  </form>

  <section class="certificates" aria-labelledby="certificates-title">
    <h2 id="certificates-title">Certificates</h2>
    <div class="switch-row">
      <button
        type="button"
        role="switch"
        class="switch"
        aria-checked={form.certificates.trust_ca}
        aria-describedby="trust-risk"
        disabled={!$running || switching}
        onclick={toggleTrust}
      >
        <span class="knob" aria-hidden="true"></span>
        <span>Trust the hlab CA</span>
      </button>
    </div>
    <p id="trust-risk" class="small muted">
      Adds the hlab CA certificate to the Windows trust store and to the WSL2 distros; switching off removes it. The CA
      cannot be limited to hlab's domains: if its key were stolen while trust is on, it could be used to forge TLS
      certificates for any domain. Keep it off unless you need it.
    </p>
    <Button onclick={saveCA} disabled={!$running}>Save CA certificate</Button>
    {#if !$running}<p class="small muted">Start the cloud to switch trust or save the CA certificate.</p>{/if}
  </section>
{:else}
  <p class="muted">Loading settings…</p>
{/if}

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  form,
  .certificates {
    max-width: 560px;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: var(--s3) var(--s4) 0;
    margin: 0 0 var(--s4);
  }
  legend {
    padding: 0 var(--s1);
    font-weight: 600;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: var(--s3);
    margin-bottom: var(--s6);
  }
  .certificates h2 {
    margin-bottom: var(--s2);
  }
  .switch {
    display: inline-flex;
    align-items: center;
    gap: var(--s2);
    padding: 0;
    border: none;
    background: none;
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .switch:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .knob {
    position: relative;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--border);
    transition: background 0.15s;
  }
  .knob::after {
    content: "";
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--surface);
    transition: left 0.15s;
  }
  .switch[aria-checked="true"] .knob {
    background: var(--accent);
  }
  .switch[aria-checked="true"] .knob::after {
    left: 18px;
  }
</style>
