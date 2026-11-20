<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import { listAll, runApi } from "../../../lib/api";
  import { request, type Input, type ResourceAction } from "../../../lib/resourceActions";
  import { tabs } from "../../../lib/resources/loadBalancers";
  import { showError } from "../../../lib/toast";
  import ServiceEditor, { newService, serviceForm, serviceInput, type ServiceForm } from "../ServiceEditor.svelte";
  import type { LoadBalancer, LoadBalancerService } from "../Section.svelte";

  // The Services tab: the services of the load balancer; add and edit
  // through ServiceEditor (an edited service keeps its listen port, which
  // names it), delete after a confirmation.
  type Props = { project: string; lb: LoadBalancer; onchanged: () => void };

  let { project, lb, onchanged }: Props = $props();

  type Named = { id: number; name: string };
  let certificates = $state<Named[]>([]);

  $effect(() => {
    listAll<Named>(project, "/certificates", "certificates")
      .then((c) => (certificates = c.sort((a, b) => a.name.localeCompare(b.name))))
      .catch((err) => showError("Load certificates", err));
  });

  // editing is the listen port of the service being edited; undefined adds one.
  let editOpen = $state(false);
  let editing = $state<number | undefined>();
  let form = $state<ServiceForm>(newService());
  let deleteOpen = $state(false);
  let deleting = $state<number | undefined>();

  async function run(a: ResourceAction, input: Input) {
    const req = request(a, lb.id, input)!;
    const res = await runApi(project, `${a.label} ${lb.name}`, req.method, req.path, req.body);
    if (res !== undefined) onchanged();
  }

  function openAdd() {
    editing = undefined;
    form = newService();
    editOpen = true;
  }

  function openEdit(s: LoadBalancerService) {
    editing = s.listen_port;
    form = serviceForm(s);
    editOpen = true;
  }

  function save(e: SubmitEvent) {
    e.preventDefault();
    editOpen = false;
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(editing === undefined ? tabs.add_service : tabs.update_service, serviceInput(form));
  }

  function askDelete(port: number) {
    deleting = port;
    deleteOpen = true;
  }

  const checkText = (s: LoadBalancerService) => {
    const hc = s.health_check;
    const proto = hc.protocol === "http" && hc.http?.tls ? "https" : hc.protocol;
    return `${proto} ${hc.port}${hc.protocol === "http" ? ` ${hc.http?.path ?? "/"}` : ""}, every ${hc.interval} s`;
  };
  const certText = (s: LoadBalancerService) =>
    (s.http?.certificates ?? []).map((id) => certificates.find((c) => c.id === id)?.name ?? `certificate ${id}`).join(", ");
</script>

<section>
  <div class="head">
    <h2>Services</h2>
    <Button onclick={openAdd}>Add service</Button>
  </div>
  {#if lb.services.length === 0}
    <p class="muted">The load balancer has no services.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Protocol</th>
          <th>Listen port</th>
          <th>Destination port</th>
          <th>Health check</th>
          <th>Sticky sessions</th>
          <th>Certificates</th>
          <th><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each lb.services as s (s.listen_port)}
          <tr>
            <td>{s.protocol.toUpperCase()}{s.proxyprotocol ? " (proxy protocol)" : ""}</td>
            <td>{s.listen_port}</td>
            <td>{s.destination_port}</td>
            <td>{checkText(s)}</td>
            <td>{s.protocol !== "tcp" && s.http?.sticky_sessions ? "Yes" : "No"}</td>
            <td>{s.protocol === "https" ? certText(s) : "–"}</td>
            <td class="actions">
              <Button onclick={() => openEdit(s)}>Edit</Button>
              <Button variant="danger" onclick={() => askDelete(s.listen_port)}>Delete</Button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<Dialog bind:open={editOpen} title={editing === undefined ? `Add a service to ${lb.name}` : `Edit service ${editing}`}>
  <form onsubmit={save}>
    <ServiceEditor bind:service={form} {certificates} fixedPort={editing !== undefined} />
    <div class="buttons">
      <Button onclick={() => (editOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary">{editing === undefined ? "Add service" : "Save service"}</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog
  bind:open={deleteOpen}
  title={`Delete service ${deleting ?? ""}`}
  confirmLabel="Delete"
  onconfirm={() => {
    if (deleting !== undefined) run(tabs.delete_service, { listen_port: deleting });
  }}
>
  <p>Delete the service on port <strong>{deleting}</strong> of <strong>{lb.name}</strong>?</p>
</ConfirmDialog>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 960px;
    margin-bottom: var(--s3);
  }
  table {
    border-collapse: collapse;
    max-width: 960px;
    width: 100%;
  }
  th,
  td {
    text-align: left;
    padding: var(--s2);
    border-bottom: 1px solid var(--border);
  }
  .actions {
    display: flex;
    gap: var(--s2);
    justify-content: flex-end;
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
