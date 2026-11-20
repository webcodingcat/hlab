<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import { untrack } from "svelte";
  import Button from "../../../lib/Button.svelte";
  import { runAction, type Action, type ActionResult } from "../../../lib/actions";
  import { api } from "../../../lib/api";
  import { formErrors } from "../../../lib/formErrors";
  import { request } from "../../../lib/resourceActions";
  import { tabs } from "../../../lib/resources/zones";
  import type { Zone } from "../Section.svelte";

  // The Primary nameservers tab of a secondary zone: address, port and an
  // optional TSIG algorithm and key per nameserver; "Save primary
  // nameservers" replaces the list through change_primary_nameservers.
  let { project, zone, onchanged }: { project: string; zone: Zone; onchanged: () => void } = $props();

  type Row = { address: string; port: number | null; tsig_algorithm: string; tsig_key: string };

  const algorithms = ["hmac-md5", "hmac-sha1", "hmac-sha256"];
  const action = tabs.change_primary_nameservers;

  // The rows start from the zone as the tab opens; the 5 s refresh of the
  // zone does not overwrite edits.
  let rows = $state<Row[]>(
    untrack(() =>
      zone.primary_nameservers.map((n) => ({
        address: n.address,
        port: n.port,
        tsig_algorithm: n.tsig_algorithm ?? "",
        tsig_key: n.tsig_key ?? "",
      })),
    ),
  );
  let error = $state<string | undefined>();
  let busy = $state(false);

  function addRow() {
    rows = [...rows, { address: "", port: 53, tsig_algorithm: "", tsig_key: "" }];
  }

  function removeRow(i: number) {
    rows = rows.filter((_, j) => j !== i);
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    error = undefined;
    const primary_nameservers = rows
      .filter((r) => r.address.trim() !== "")
      .map((r) => ({
        address: r.address.trim(),
        port: r.port ?? 53,
        ...(r.tsig_algorithm ? { tsig_algorithm: r.tsig_algorithm, tsig_key: r.tsig_key.trim() } : {}),
      }));
    const req = request(action, zone.id, { primary_nameservers })!;
    busy = true;
    const res = await runAction(
      `${action.label} ${zone.name}`,
      async () => {
        try {
          return await api<ActionResult>(project, req.method, req.path, req.body);
        } catch (err) {
          const f = formErrors(err, ["primary_nameservers"]);
          error = f.fields.primary_nameservers ?? f.general;
          throw err;
        } finally {
          busy = false;
        }
      },
      { get: async (id) => (await api<{ action: Action }>(project, "GET", `/actions/${id}`)).action },
    );
    if (res !== undefined) onchanged();
  }
</script>

<form onsubmit={save}>
  <p class="muted">HLab stores the primary nameservers but never transfers the zone from them.</p>
  {#each rows as _, i (i)}
    <div class="row">
      <input
        aria-label={`Address ${i + 1}`}
        placeholder="198.51.100.53"
        autocomplete="off"
        spellcheck="false"
        bind:value={rows[i].address}
      />
      <input class="port" aria-label={`Port ${i + 1}`} type="number" min="1" max="65535" bind:value={rows[i].port} />
      <select aria-label={`TSIG algorithm ${i + 1}`} bind:value={rows[i].tsig_algorithm}>
        <option value="">No TSIG</option>
        {#each algorithms as a (a)}<option value={a}>{a}</option>{/each}
      </select>
      <input
        aria-label={`TSIG key ${i + 1}`}
        placeholder="TSIG key (base64)"
        autocomplete="off"
        spellcheck="false"
        disabled={!rows[i].tsig_algorithm}
        bind:value={rows[i].tsig_key}
      />
      <Button variant="icon" label={`Remove nameserver ${i + 1}`} onclick={() => removeRow(i)}>
        <X size={16} aria-hidden="true" />
      </Button>
    </div>
  {/each}
  <div>
    <Button onclick={addRow}><Plus size={16} aria-hidden="true" />Add nameserver</Button>
  </div>
  {#if error}<div class="error">{error}</div>{/if}
  <div class="actions">
    <Button type="submit" variant="primary" loading={busy}>Save primary nameservers</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    max-width: 800px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .row input,
  .row select {
    flex: 1;
    min-width: 0;
  }
  .row .port {
    flex: 0 0 7em;
  }
  .muted {
    color: var(--muted);
    margin: 0;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
