<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import { listAll, runApi } from "../../../lib/api";
  import { request, type Input, type ResourceAction } from "../../../lib/resourceActions";
  import { tabs } from "../../../lib/resources/loadBalancers";
  import { showError } from "../../../lib/toast";
  import type { LoadBalancer, LoadBalancerTarget } from "../Section.svelte";
  import TargetEditor, { newTarget, targetInput, type TargetForm } from "../TargetEditor.svelte";

  // The Targets tab: the targets of the load balancer with their health per
  // service (a label selector lists the servers it matched under it); add
  // through TargetEditor, remove after a confirmation.
  type Props = { project: string; lb: LoadBalancer; onchanged: () => void };

  let { project, lb, onchanged }: Props = $props();

  type Named = { id: number; name: string };
  let servers = $state<Named[]>([]);

  $effect(() => {
    listAll<Named>(project, "/servers", "servers")
      .then((s) => (servers = s.sort((a, b) => a.name.localeCompare(b.name))))
      .catch((err) => showError("Load servers", err));
  });

  let addOpen = $state(false);
  let form = $state<TargetForm>(newTarget());
  let removeOpen = $state(false);
  let removing = $state<LoadBalancerTarget | undefined>();

  const ports = $derived(lb.services.map((s) => s.listen_port));

  function targetText(t: LoadBalancerTarget): string {
    if (t.type === "server") return servers.find((s) => s.id === t.server?.id)?.name ?? `server ${t.server?.id}`;
    if (t.type === "label_selector") return `Label selector ${t.label_selector?.selector ?? ""}`;
    return t.ip?.ip ?? "";
  }

  const healthOf = (t: LoadBalancerTarget, port: number) => t.health_status?.find((h) => h.listen_port === port)?.status ?? "unknown";

  // removeInput names the target as remove_target takes it.
  function removeInput(t: LoadBalancerTarget): Input {
    if (t.type === "server") return { type: "server", server: { id: t.server?.id } };
    if (t.type === "label_selector") return { type: "label_selector", label_selector: { selector: t.label_selector?.selector } };
    return { type: "ip", ip: { ip: t.ip?.ip } };
  }

  async function run(a: ResourceAction, input: Input) {
    const req = request(a, lb.id, input)!;
    const res = await runApi(project, `${a.label} ${lb.name}`, req.method, req.path, req.body);
    if (res !== undefined) onchanged();
  }

  function openAdd() {
    form = newTarget();
    addOpen = true;
  }

  const ready = $derived(
    (form.type === "server" && form.server !== "") ||
      (form.type === "label_selector" && form.selector.trim() !== "") ||
      (form.type === "ip" && form.ip.trim() !== ""),
  );

  function add(e: SubmitEvent) {
    e.preventDefault();
    if (!ready) return;
    addOpen = false;
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(tabs.add_target, targetInput(form));
  }

  function askRemove(t: LoadBalancerTarget) {
    removing = t;
    removeOpen = true;
  }
</script>

{#snippet healthCells(t: LoadBalancerTarget)}
  {#each ports as p (p)}
    {@const st = healthOf(t, p)}
    <td><span class={["health", st]}>{st}</span></td>
  {/each}
{/snippet}

<section>
  <div class="head">
    <h2>Targets</h2>
    <Button onclick={openAdd}>Add target</Button>
  </div>
  {#if lb.targets.length === 0}
    <p class="muted">The load balancer has no targets.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Target</th>
          <th>Private network</th>
          {#each ports as p (p)}<th>Port {p}</th>{/each}
          <th><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each lb.targets as t, i (i)}
          <tr>
            <td>{targetText(t)}</td>
            <td>{t.type === "ip" ? "–" : t.use_private_ip ? "Yes" : "No"}</td>
            {#if t.type === "label_selector"}
              {#each ports as p (p)}<td></td>{/each}
            {:else}
              {@render healthCells(t)}
            {/if}
            <td><Button variant="danger" onclick={() => askRemove(t)}>Remove</Button></td>
          </tr>
          {#each t.type === "label_selector" ? (t.targets ?? []) : [] as m, j (j)}
            <tr class="matched">
              <td>{targetText(m)}</td>
              <td>{m.use_private_ip ? "Yes" : "No"}</td>
              {@render healthCells(m)}
              <td></td>
            </tr>
          {/each}
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={addOpen} title={`Add a target to ${lb.name}`}>
  <form onsubmit={add}>
    <TargetEditor bind:target={form} {servers} />
    <div class="buttons">
      <Button onclick={() => (addOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={!ready}>Add target</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={removeOpen}
  title={`Remove a target from ${lb.name}`}
  confirmLabel="Remove"
  onconfirm={() => {
    if (removing) run(tabs.remove_target, removeInput(removing));
  }}
>
  <p>Remove <strong>{removing ? targetText(removing) : ""}</strong> from <strong>{lb.name}</strong>?</p>
</ConfirmDialog>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 800px;
    margin-bottom: var(--s3);
  }
  table {
    border-collapse: collapse;
    max-width: 800px;
    width: 100%;
  }
  th,
  td {
    text-align: left;
    padding: var(--s2);
    border-bottom: 1px solid var(--border);
  }
  .matched td:first-child {
    padding-left: var(--s6);
    color: var(--muted);
  }
  .health.healthy {
    color: var(--success);
  }
  .health.unhealthy {
    color: var(--error);
  }
  .health.unknown {
    color: var(--muted);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
  .muted {
    color: var(--muted);
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
</style>
