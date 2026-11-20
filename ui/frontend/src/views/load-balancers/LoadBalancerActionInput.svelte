<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import type { Input, ResourceAction } from "../../lib/resourceActions";
  import type { LoadBalancer } from "./Section.svelte";

  // The input of a load balancer action dialog: Change type asks for another
  // type, Change algorithm for the algorithm, Change protection for the
  // delete flag. It is created anew each time a dialog opens, so it starts
  // from the load balancer's current values.
  type LoadBalancerType = { id: number; name: string; description: string };
  type Props = {
    action: ResourceAction;
    lb: LoadBalancer;
    types: LoadBalancerType[];
    submit: (i: Input) => void;
    cancel: () => void;
  };

  let { action, lb, types, submit, cancel }: Props = $props();

  let type = $state("");
  let algorithm = $state(untrack(() => lb.algorithm.type));
  let protectDelete = $state(untrack(() => lb.protection.delete));

  const others = $derived(types.filter((t) => t.name !== lb.load_balancer_type.name));
  const invalid = $derived(action.id === "change_type" && type === "");

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    if (action.id === "change_type") submit({ load_balancer_type: type });
    else if (action.id === "change_algorithm") submit({ type: algorithm });
    else submit({ delete: protectDelete });
  }
</script>

<form onsubmit={send}>
  {#if action.id === "change_type"}
    <Field label="Type" hint={`Currently ${lb.load_balancer_type.name}.`}>
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={type}>
          <option value="" disabled>Choose a type</option>
          {#each others as t (t.id)}<option value={t.name}>{t.name} ({t.description})</option>{/each}
        </select>
      {/snippet}
    </Field>
  {:else if action.id === "change_algorithm"}
    <fieldset>
      <legend>Algorithm</legend>
      <label class="check"><input type="radio" name="algorithm" value="round_robin" bind:group={algorithm} />Round robin</label>
      <label class="check"
        ><input type="radio" name="algorithm" value="least_connections" bind:group={algorithm} />Least connections</label
      >
    </fieldset>
  {:else}
    <label class="check"><input type="checkbox" bind:checked={protectDelete} />Protect against deletion</label>
  {/if}
  <div class="buttons">
    <Button onclick={cancel}>Cancel</Button>
    <Button type="submit" variant="primary" disabled={invalid}>{action.label}</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
  }
  select {
    width: 100%;
  }
  fieldset {
    border: 0;
    margin: 0;
    padding: 0;
  }
  legend {
    font-weight: 600;
    margin-bottom: var(--s2);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
