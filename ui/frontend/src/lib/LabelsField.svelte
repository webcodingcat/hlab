<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import { toLabels, type LabelRow } from "./labels";

  // Label rows of a form: key, value and remove per row, "Add label" below.
  // A key used twice shows under its second row; error (the API's labels
  // message) shows under the list.
  type Props = { rows: LabelRow[]; error?: string };

  let { rows = $bindable(), error }: Props = $props();

  const id = $props.id();
  const duplicate = $derived.by(() => {
    const r = toLabels(rows);
    return "error" in r ? r : undefined;
  });

  function add() {
    rows = [...rows, { key: "", value: "" }];
  }

  function remove(i: number) {
    rows = rows.filter((_, j) => j !== i);
  }
</script>

<fieldset class="labels" aria-describedby={error ? `${id}-error` : undefined}>
  <legend>Labels</legend>
  {#each rows as row, i (i)}
    <div class="row">
      <input
        aria-label={`Label ${i + 1} key`}
        placeholder="key"
        autocomplete="off"
        aria-invalid={duplicate?.row === i}
        aria-describedby={duplicate?.row === i ? `${id}-dup` : undefined}
        bind:value={row.key}
      />
      <input aria-label={`Label ${i + 1} value`} placeholder="value" autocomplete="off" bind:value={row.value} />
      <Button variant="icon" label={`Remove label ${i + 1}`} onclick={() => remove(i)}>
        <X size={16} aria-hidden="true" />
      </Button>
    </div>
    {#if duplicate?.row === i}<div id={`${id}-dup`} class="error">{duplicate.error}</div>{/if}
  {/each}
  <div>
    <Button onclick={add}><Plus size={16} aria-hidden="true" />Add label</Button>
  </div>
  {#if error}<div id={`${id}-error`} class="error">{error}</div>{/if}
</fieldset>

<style>
  .labels {
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
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr auto;
    gap: var(--s2);
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
