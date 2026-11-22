<script lang="ts">
  import { parseLabel, type LabelRow } from "./labels";

  // The labels of a form as chips in one box (UI-SPEC "Labels field"):
  // "key=value" or "key" becomes a chip on Enter, Space or comma (and when
  // the box loses focus, so typed text is not lost on submit); a click
  // selects a chip, Delete or Backspace removes it, Backspace in the empty
  // input removes the last chip, a double click puts it back into the input.
  // A broken rule or a key used twice shows under the box and the text stays;
  // the input is then marked invalid (setCustomValidity), so the enclosing
  // form refuses a submit while the error line stays, without the browser's
  // bubble. error (the API's labels message) shows there too.
  type Props = { rows: LabelRow[]; error?: string };

  let { rows = $bindable(), error }: Props = $props();

  const id = $props.id();
  let text = $state("");
  let selected = $state<number | undefined>();
  let message = $state<string | undefined>();
  let input = $state<HTMLInputElement>();

  /** fail shows m under the box and marks the input invalid; undefined clears both. */
  function fail(m: string | undefined) {
    message = m;
    input?.setCustomValidity(m ?? "");
  }

  /** commit turns the typed text into a chip; it keeps the text on an error. */
  function commit() {
    if (text.trim() === "") {
      text = "";
      fail(undefined);
      return;
    }
    const r = parseLabel(text);
    if ("error" in r) {
      fail(r.error);
      return;
    }
    if (rows.some((x) => x.key === r.row.key)) {
      fail(`Label key "${r.row.key}" is used twice.`);
      return;
    }
    rows = [...rows, r.row];
    text = "";
    fail(undefined);
  }

  /** invalid keeps the browser's bubble away and puts the user back in the box. */
  function invalid(e: Event) {
    e.preventDefault();
    input?.focus();
  }

  function remove(i: number) {
    rows = rows.filter((_, j) => j !== i);
    selected = undefined;
    input?.focus();
  }

  function edit(i: number) {
    const r = rows[i];
    remove(i);
    text = r.value === "" ? r.key : `${r.key}=${r.value}`;
    fail(undefined);
  }

  function inputKey(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " " || e.key === ",") {
      e.preventDefault();
      commit();
    } else if (e.key === "Backspace" && text === "" && rows.length > 0) {
      e.preventDefault();
      remove(rows.length - 1);
    }
  }

  function chipKey(e: KeyboardEvent, i: number) {
    if (e.key === "Delete" || e.key === "Backspace") {
      e.preventDefault();
      remove(i);
    }
  }

  function boxClick(e: MouseEvent) {
    if (e.target === e.currentTarget) input?.focus();
  }

  const shown = $derived(message ?? error);
</script>

<fieldset class="labels">
  <legend>Labels</legend>
  <p id={`${id}-rule`} class="rule small muted">
    Labels are key-value pairs. Key and value are at most 63 characters each, start and end with an alphanumeric
    character and may contain dashes, underscores and dots in between. The value is optional.
  </p>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="box" class:invalid={shown} onclick={boxClick}>
    {#each rows as row, i (i)}
      <button
        type="button"
        class="chip"
        class:selected={selected === i}
        aria-label={row.value === "" ? row.key : `${row.key}=${row.value}`}
        aria-pressed={selected === i}
        onclick={() => (selected = i)}
        ondblclick={() => edit(i)}
        onkeydown={(e) => chipKey(e, i)}
        onblur={() => {
          if (selected === i) selected = undefined;
        }}
      >
        <span class="key">{row.key}</span>{#if row.value !== ""}<span class="value">{row.value}</span>{/if}
      </button>
    {/each}
    <input
      bind:this={input}
      bind:value={text}
      aria-label="Labels"
      autocomplete="off"
      spellcheck="false"
      aria-invalid={message !== undefined}
      aria-describedby={shown ? `${id}-rule ${id}-error` : `${id}-rule`}
      onkeydown={inputKey}
      onblur={commit}
      oninvalid={invalid}
    />
  </div>
  {#if shown}<div id={`${id}-error`} class="error">{shown}</div>{/if}
</fieldset>

<style>
  .labels {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    margin: 0 0 var(--s4);
    padding: 0;
    border: none;
    min-width: 0;
  }
  legend {
    margin-bottom: var(--s1);
    font-weight: 600;
  }
  .rule {
    margin: 0;
  }
  .box {
    display: flex;
    flex-wrap: wrap;
    align-content: flex-start;
    align-items: center;
    gap: var(--s2);
    min-height: 112px;
    padding: var(--s2);
    overflow: auto;
    resize: vertical;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    cursor: text;
  }
  .box:focus-within {
    border-color: var(--accent);
  }
  .box.invalid {
    border-color: var(--error);
  }
  .chip {
    display: inline-flex;
    padding: 0;
    font: inherit;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    cursor: pointer;
  }
  .chip span {
    padding: 0 var(--s2);
    line-height: 22px;
  }
  .key {
    background: color-mix(in srgb, var(--muted) 20%, transparent);
  }
  .chip.selected {
    border-color: var(--accent);
  }
  .chip.selected .key {
    background: var(--accent);
    color: #ffffff;
  }
  input {
    flex: 1 1 120px;
    min-width: 120px;
    min-height: 24px;
    padding: 0;
    border: none;
    background: transparent;
  }
  input:focus-visible {
    outline: none;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
