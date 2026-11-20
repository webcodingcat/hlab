<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import Button from "../../lib/Button.svelte";
  import type { RuleRow } from "../../lib/resources/firewallRules";

  // The rules of a firewall as two tables, inbound first, then outbound: per
  // rule protocol, port (TCP and UDP only), IPs (source IPs inbound,
  // destination IPs outbound; separated by commas or line breaks) and
  // description. error is the API's rules message, shown under the tables.
  type Props = { rows: RuleRow[]; error?: string };

  let { rows = $bindable(), error }: Props = $props();

  const protocols: RuleRow["protocol"][] = ["tcp", "udp", "icmp", "esp", "gre"];
  const directions = [
    { direction: "in", title: "Inbound rules", add: "Add inbound rule", ips: "Source IPs" },
    { direction: "out", title: "Outbound rules", add: "Add outbound rule", ips: "Destination IPs" },
  ] as const;

  function add(direction: RuleRow["direction"]) {
    rows = [...rows, { direction, protocol: "tcp", port: "", ips: "0.0.0.0/0, ::/0", description: "" }];
  }

  function remove(i: number) {
    rows = rows.filter((_, j) => j !== i);
  }
</script>

{#each directions as d (d.direction)}
  <fieldset>
    <legend>{d.title}</legend>
    {#if rows.some((r) => r.direction === d.direction)}
      <table>
        <thead>
          <tr>
            <th>Protocol</th><th>Port</th><th>{d.ips}</th><th>Description</th><th
              ><span class="hidden">Remove</span></th
            >
          </tr>
        </thead>
        <tbody>
          {#each rows as row, i (i)}
            {#if row.direction === d.direction}
              <tr>
                <td>
                  <select aria-label="Protocol" bind:value={row.protocol}>
                    {#each protocols as p (p)}<option value={p}>{p.toUpperCase()}</option>{/each}
                  </select>
                </td>
                <td>
                  <input
                    aria-label="Port"
                    placeholder={row.protocol === "tcp" || row.protocol === "udp" ? "80 or 1024-2048" : "-"}
                    autocomplete="off"
                    disabled={row.protocol !== "tcp" && row.protocol !== "udp"}
                    bind:value={row.port}
                  />
                </td>
                <td>
                  <input aria-label={d.ips} autocomplete="off" spellcheck="false" bind:value={row.ips} />
                </td>
                <td><input aria-label="Description" autocomplete="off" bind:value={row.description} /></td>
                <td>
                  <Button variant="icon" label="Remove rule" onclick={() => remove(i)}>
                    <X size={16} aria-hidden="true" />
                  </Button>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="muted">No {d.direction === "in" ? "inbound" : "outbound"} rules.</p>
    {/if}
    <div>
      <Button onclick={() => add(d.direction)}><Plus size={16} aria-hidden="true" />{d.add}</Button>
    </div>
  </fieldset>
{/each}
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
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: left;
    padding: var(--s1);
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 12px;
  }
  input,
  select {
    width: 100%;
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
  .hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
</style>
