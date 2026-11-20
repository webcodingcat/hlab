<script lang="ts">
  import { untrack } from "svelte";
  import Button from "./Button.svelte";
  import { runApi } from "./api";

  // The Protection tab of a detail page: one switch per flag; "Save" sends
  // POST <path>/actions/change_protection as an action.
  type Flag = { key: string; label: string; value: boolean };
  type Props = { project: string; path: string; flags: Flag[]; onchanged: () => void };

  let { project, path, flags, onchanged }: Props = $props();

  let values = $state<Record<string, boolean>>({});
  // base is the last state of the flags shown; values that still equal it are unedited.
  let base = "{}";
  let saving = $state(false);

  // The refreshed flags replace the switches as long as nothing is changed.
  $effect(() => {
    const next = Object.fromEntries(flags.map((f) => [f.key, f.value]));
    untrack(() => {
      if (JSON.stringify(values) === base) values = next;
      base = JSON.stringify(next);
    });
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    const body = { ...values };
    const res = await runApi(project, "Change protection", "POST", `${path}/actions/change_protection`, body);
    saving = false;
    if (res === undefined) return;
    base = JSON.stringify(body);
    onchanged();
  }
</script>

<form onsubmit={save}>
  {#each flags as f (f.key)}
    <button
      type="button"
      role="switch"
      class="switch"
      aria-checked={values[f.key] ?? false}
      onclick={() => (values[f.key] = !values[f.key])}
    >
      <span class="knob" aria-hidden="true"></span>
      <span>{f.label}</span>
    </button>
  {/each}
  <div class="actions">
    <Button type="submit" variant="primary" loading={saving}>Save</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--s3);
    max-width: 640px;
  }
  .actions {
    align-self: stretch;
    display: flex;
    justify-content: flex-end;
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
  .switch:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
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
