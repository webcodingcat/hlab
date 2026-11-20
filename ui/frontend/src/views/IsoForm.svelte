<script module lang="ts">
  /** IsoSpec is the form's state of an own ISO; id 0 adds a new one. */
  export type IsoSpec = {
    id: number;
    name: string;
    architecture: "x86" | "arm";
    source: "url" | "path";
    url: string;
    sha256: string;
    path: string;
  };
</script>

<script lang="ts">
  import Button from "../lib/Button.svelte";
  import Field from "../lib/Field.svelte";
  import { pickISOFile } from "../lib/bridge";
  import { showError } from "../lib/toast";

  // The add and edit form of an own ISO. The parent saves it; errors are
  // keyed by the fields of catalog.OwnISOSpec (description is the Name).
  type Props = {
    spec: IsoSpec;
    errors: Record<string, string>;
    saving: boolean;
    onsubmit: (e: SubmitEvent) => void;
    oncancel: () => void;
  };

  let { spec = $bindable(), errors, saving, onsubmit, oncancel }: Props = $props();

  async function browse() {
    try {
      const path = await pickISOFile();
      if (path) spec.path = path;
    } catch (err) {
      showError("Select ISO", err);
    }
  }
</script>

<form {onsubmit} novalidate>
  <Field label="Name" error={errors.description}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.name} />
    {/snippet}
  </Field>
  <Field label="Architecture" error={errors.architecture}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={spec.architecture}>
        <option value="x86">x86</option>
        <option value="arm">arm</option>
      </select>
    {/snippet}
  </Field>
  <fieldset class="source" role="radiogroup" aria-label="Source">
    <legend>Source</legend>
    <label class="check"><input type="radio" name="source" value="url" bind:group={spec.source} /> URL</label>
    <label class="check"><input type="radio" name="source" value="path" bind:group={spec.source} /> Local file</label>
  </fieldset>
  {#if spec.source === "url"}
    <Field label="URL" error={errors.url}>
      {#snippet children(c)}
        <input id={c.id} type="url" aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.url} />
      {/snippet}
    </Field>
    <Field label="SHA-256" error={errors.sha256} hint="Optional; the download is checked against it.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.sha256} />
      {/snippet}
    </Field>
  {:else}
    <Field label="Path" error={errors.path} hint="A local .iso file; HLab copies it when the cloud reads the own ISOs.">
      {#snippet children(c)}
        <div class="path">
          <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={spec.path} />
          <Button onclick={browse}>Browse…</Button>
        </div>
      {/snippet}
    </Field>
  {/if}
  <div class="actions">
    <Button onclick={oncancel} disabled={saving}>Cancel</Button>
    <Button type="submit" variant="primary" loading={saving}>{spec.id ? "Save" : "Add"}</Button>
  </div>
</form>

<style>
  input,
  select {
    width: 100%;
  }
  .source {
    display: flex;
    gap: var(--s4);
    margin: 0 0 var(--s4);
    padding: 0;
    border: 0;
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
  .check input {
    width: auto;
  }
  .path {
    display: flex;
    gap: var(--s2);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
  }
</style>
