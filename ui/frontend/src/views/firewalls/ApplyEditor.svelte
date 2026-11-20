<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import Button from "../../lib/Button.svelte";
  import { listAll } from "../../lib/api";
  import { showError } from "../../lib/toast";

  // The resources a firewall applies to: servers of the project (chosen by
  // check box, by ID) and label selectors (one expression per row). error is
  // the API's apply_to message, shown under the lists.
  type Props = { project: string; servers: number[]; selectors: string[]; error?: string };

  let { project, servers = $bindable(), selectors = $bindable(), error }: Props = $props();

  let all = $state<{ id: number; name: string }[] | undefined>();

  $effect(() => {
    listAll<{ id: number; name: string }>(project, "/servers", "servers")
      .then((s) => (all = s))
      .catch((err) => showError("Load servers", err));
  });

  function add() {
    selectors = [...selectors, ""];
  }

  function remove(i: number) {
    selectors = selectors.filter((_, j) => j !== i);
  }
</script>

<fieldset>
  <legend>Apply to servers</legend>
  {#each all ?? [] as s (s.id)}
    <label class="check"><input type="checkbox" value={s.id} bind:group={servers} />{s.name}</label>
  {:else}
    <p class="muted">{all ? "This project has no servers." : "Loading servers…"}</p>
  {/each}
</fieldset>

<fieldset>
  <legend>Apply to label selectors</legend>
  {#each selectors as _, i (i)}
    <div class="row">
      <input
        aria-label={`Label selector ${i + 1}`}
        placeholder="env=prod"
        autocomplete="off"
        spellcheck="false"
        bind:value={selectors[i]}
      />
      <Button variant="icon" label={`Remove label selector ${i + 1}`} onclick={() => remove(i)}>
        <X size={16} aria-hidden="true" />
      </Button>
    </div>
  {/each}
  <div>
    <Button onclick={add}><Plus size={16} aria-hidden="true" />Add label selector</Button>
  </div>
</fieldset>
{#if error}<div class="error">{error}</div>{/if}

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
  .row {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
    margin-bottom: var(--s4);
  }
</style>
