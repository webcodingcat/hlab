<script module lang="ts">
  import type { Input } from "../../lib/resourceActions";

  /** TargetForm holds the fields of every target kind; targetInput sends only its kind's. */
  export type TargetForm = {
    type: "server" | "label_selector" | "ip";
    server: string;
    selector: string;
    ip: string;
    use_private_ip: boolean;
  };

  export function newTarget(): TargetForm {
    return { type: "server", server: "", selector: "", ip: "", use_private_ip: false };
  }

  /** targetInput is the target as the actions module takes it. */
  export function targetInput(t: TargetForm): Input {
    if (t.type === "server") return { type: "server", server: { id: Number(t.server) }, use_private_ip: t.use_private_ip };
    if (t.type === "label_selector") {
      return { type: "label_selector", label_selector: { selector: t.selector.trim() }, use_private_ip: t.use_private_ip };
    }
    return { type: "ip", ip: { ip: t.ip.trim() } };
  }
</script>

<script lang="ts">
  import Field from "../../lib/Field.svelte";

  // One load balancer target: a server of the project, a label selector or
  // an IP. Servers and label selectors can be reached over the private
  // network. Used by the create form and the Targets tab.
  type Props = { target: TargetForm; servers: { id: number; name: string }[] };

  let { target = $bindable(), servers }: Props = $props();
</script>

<div class="target">
  <Field label="Target type">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} bind:value={target.type}>
        <option value="server">Server</option>
        <option value="label_selector">Label selector</option>
        <option value="ip">IP</option>
      </select>
    {/snippet}
  </Field>
  {#if target.type === "server"}
    <Field label="Server">
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={target.server}>
          <option value="" disabled>Choose a server</option>
          {#each servers as s (s.id)}<option value={`${s.id}`}>{s.name}</option>{/each}
        </select>
      {/snippet}
    </Field>
  {:else if target.type === "label_selector"}
    <Field label="Label selector" hint="For example app=web.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={target.selector} />
      {/snippet}
    </Field>
  {:else}
    <Field label="IP">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={target.ip} />
      {/snippet}
    </Field>
  {/if}
  {#if target.type !== "ip"}
    <label class="check"><input type="checkbox" bind:checked={target.use_private_ip} />Use the private network</label>
  {/if}
</div>

<style>
  .target {
    display: flex;
    flex-direction: column;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s3);
  }
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
</style>
