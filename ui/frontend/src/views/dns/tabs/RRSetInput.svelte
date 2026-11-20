<script module lang="ts">
  /** RRSet as the Hetzner API returns it (the fields the views use). */
  export type RRSet = {
    id: string;
    name: string;
    type: string;
    ttl: number | null;
    labels: Record<string, string>;
    protection: { change: boolean };
    records: { value: string; comment?: string }[];
  };

  /** recordTypes are the RRSet types a new record set can have (the SOA is HLab's). */
  export const recordTypes = ["A", "AAAA", "CAA", "CNAME", "DS", "HINFO", "HTTPS", "MX", "NS", "PTR", "RP", "SRV", "SVCB", "TLSA", "TXT"];
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../../lib/Button.svelte";
  import Field from "../../../lib/Field.svelte";
  import LabelsField from "../../../lib/LabelsField.svelte";
  import { toLabels, toRows, type LabelRow } from "../../../lib/labels";
  import type { Input, ResourceAction } from "../../../lib/resourceActions";

  // The input of an RRSet dialog: Add record set asks for name, type, TTL,
  // records and labels; Change TTL for the TTL (empty: the zone's default);
  // Add records for new values; Remove records for the values to remove;
  // Change labels for the labels; Change protection for the change flag.
  // It is created anew each time a dialog opens, so it starts from the
  // RRSet's current values.
  type Props = { action: ResourceAction; rrset?: RRSet; submit: (i: Input) => void; cancel: () => void };

  let { action, rrset, submit, cancel }: Props = $props();

  const initial = untrack(() => rrset);
  let name = $state("@");
  let type = $state("A");
  let ttl = $state<number | null>(initial?.ttl ?? null);
  let values = $state("");
  let removed = $state<string[]>([]);
  let labelRows = $state<LabelRow[]>(toRows(initial?.labels ?? {}));
  let protect = $state(initial?.protection.change ?? false);

  // lines are the record values entered, one per line.
  const lines = $derived(
    values
      .split("\n")
      .map((v) => v.trim())
      .filter((v) => v !== ""),
  );
  const records = (vs: string[]) => vs.map((value) => ({ value }));

  const invalid = $derived.by(() => {
    switch (action.id) {
      case "create_rrset":
        return name.trim() === "" || lines.length === 0;
      case "rrset_add_records":
        return lines.length === 0;
      case "rrset_remove_records":
        return removed.length === 0;
      case "rrset_labels":
        return "error" in toLabels(labelRows);
      default:
        return false;
    }
  });

  function send(e: SubmitEvent) {
    e.preventDefault();
    if (invalid) return;
    const l = toLabels(labelRows);
    const labels = "error" in l ? {} : l.labels;
    switch (action.id) {
      case "create_rrset": {
        const i: Input = { name: name.trim(), type, records: records(lines), labels };
        if (ttl != null) i.ttl = ttl;
        submit(i);
        break;
      }
      case "rrset_change_ttl":
        submit({ ttl: ttl ?? null });
        break;
      case "rrset_add_records":
        submit({ records: records(lines) });
        break;
      case "rrset_remove_records":
        submit({ records: records(removed) });
        break;
      case "rrset_labels":
        submit({ labels });
        break;
      default:
        submit({ change: protect });
    }
  }
</script>

<form onsubmit={send}>
  {#if action.id === "create_rrset"}
    <Field label="Name" hint="Relative to the zone; @ is the zone itself, * a wildcard.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} autocomplete="off" spellcheck="false" bind:value={name} />
      {/snippet}
    </Field>
    <Field label="Type">
      {#snippet children(c)}
        <select id={c.id} bind:value={type}>
          {#each recordTypes as t (t)}<option value={t}>{t}</option>{/each}
        </select>
      {/snippet}
    </Field>
  {/if}
  {#if action.id === "create_rrset" || action.id === "rrset_change_ttl"}
    <Field label="TTL" hint="In seconds, at least 60; empty uses the zone's default TTL.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} type="number" min="60" bind:value={ttl} />
      {/snippet}
    </Field>
  {/if}
  {#if action.id === "create_rrset" || action.id === "rrset_add_records"}
    <Field label="Records" hint="One value per line, for example 198.51.100.10 or &quot;v=spf1 -all&quot;.">
      {#snippet children(c)}
        <textarea id={c.id} aria-describedby={c.describedby} rows="5" spellcheck="false" bind:value={values}></textarea>
      {/snippet}
    </Field>
  {/if}
  {#if action.id === "create_rrset" || action.id === "rrset_labels"}
    <LabelsField bind:rows={labelRows} />
  {/if}
  {#if action.id === "rrset_remove_records" && rrset}
    <fieldset>
      <legend>Records to remove</legend>
      {#each rrset.records as r (r.value)}
        <label class="check mono"><input type="checkbox" value={r.value} bind:group={removed} />{r.value}</label>
      {/each}
    </fieldset>
  {/if}
  {#if action.id === "rrset_change_protection"}
    <label class="check"><input type="checkbox" bind:checked={protect} />Protect against changes</label>
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
  input:not([type="checkbox"]),
  select,
  textarea {
    width: 100%;
  }
  textarea,
  .mono {
    font-family: var(--mono);
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    margin: 0;
    padding: 0;
    border: 0;
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
