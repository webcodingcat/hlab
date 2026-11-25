<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import Button from "../../lib/Button.svelte";
  import Field from "../../lib/Field.svelte";
  import FormPage from "../../lib/FormPage.svelte";
  import LabelsField from "../../lib/LabelsField.svelte";
  import { listAll, runOn } from "../../lib/api";
  import { formErrors, type FormErrors } from "../../lib/formErrors";
  import { toLabels, type LabelRow } from "../../lib/labels";
  import { request, type Input } from "../../lib/resourceActions";
  import { create } from "../../lib/resources/loadBalancers";
  import { showError } from "../../lib/toast";
  import ServiceEditor, { newService, serviceInput, type ServiceForm } from "./ServiceEditor.svelte";
  import TargetEditor, { newTarget, targetInput, type TargetForm } from "./TargetEditor.svelte";

  // The create form of a load balancer in the Hetzner Console's order:
  // location, type, network and public interface, targets, services,
  // algorithm, labels, name. The list opens as soon as the API answers; the
  // toast follows the create action.
  let { project, list }: { project: string; list: string } = $props();

  type Named = { id: number; name: string };
  type Location = Named & { city: string };
  type LoadBalancerType = Named & { description: string; max_targets: number; max_services: number };

  let locations = $state<Location[]>([]);
  let types = $state<LoadBalancerType[]>([]);
  let networks = $state<(Named & { ip_range: string })[]>([]);
  let servers = $state<Named[]>([]);
  let certificates = $state<Named[]>([]);

  const byName = <T extends Named>(a: T, b: T) => a.name.localeCompare(b.name);

  async function load() {
    try {
      const [l, t, n, s, c] = await Promise.all([
        listAll<Location>(project, "/locations", "locations"),
        listAll<LoadBalancerType>(project, "/load_balancer_types", "load_balancer_types"),
        listAll<Named & { ip_range: string }>(project, "/networks", "networks"),
        listAll<Named>(project, "/servers", "servers"),
        listAll<Named>(project, "/certificates", "certificates"),
      ]);
      locations = l.sort(byName);
      types = t.sort(byName);
      networks = n.sort(byName);
      servers = s.sort(byName);
      certificates = c.sort(byName);
      if (!location && locations.length > 0) location = locations[0].name;
      if (!type && types.length > 0) type = types[0].name;
    } catch (err) {
      showError("Load the load balancer options", err);
    }
  }

  $effect(() => {
    load();
  });

  let location = $state("");
  let type = $state("");
  let network = $state("");
  let publicInterface = $state(true);
  let targets = $state<TargetForm[]>([]);
  let services = $state<ServiceForm[]>([newService()]);
  let algorithm = $state("round_robin");
  let labelRows = $state<LabelRow[]>([]);
  let name = $state("");
  let busy = $state(false);
  let errors = $state<FormErrors>({ fields: {} });

  const known = ["location", "load_balancer_type", "network", "public_interface", "targets", "services", "algorithm", "labels", "name"];

  async function submit() {
    const l = toLabels(labelRows);
    errors = { fields: {} };
    if (targets.some((t) => t.type === "server" && !t.server)) errors.fields.targets = "Choose a server for every server target.";
    if ("error" in l || Object.keys(errors.fields).length > 0) return;
    const lbName = name.trim();
    const input: Input = {
      name: lbName,
      location,
      load_balancer_type: type,
      public_interface: publicInterface,
      algorithm: { type: algorithm },
      targets: targets.map(targetInput),
      services: services.map(serviceInput),
      labels: l.labels,
    };
    if (network) input.network = Number(network);
    const req = request(create, 0, input)!;
    busy = true;
    await runOn(project, lbName, "Create", req, {
      onanswer: () => {
        busy = false;
        window.location.hash = list;
      },
      onerror: (err) => {
        busy = false;
        errors = formErrors(err, known);
      },
    });
  }
</script>

<FormPage
  title="Create load balancer"
  help="load-balancers"
  submitLabel="Create load balancer"
  {busy}
  general={errors.general}
  onsubmit={submit}
  oncancel={() => (window.location.hash = list)}
>
  <Field label="Location" error={errors.fields.location}>
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={location}>
        {#each locations as l (l.id)}<option value={l.name}>{l.name} ({l.city})</option>{/each}
      </select>
    {/snippet}
  </Field>
  <Field label="Type" error={errors.fields.load_balancer_type} hint="The type can be changed later.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={type}>
        {#each types as t (t.id)}
          <option value={t.name}>{t.description} ({t.max_targets} targets, {t.max_services} services)</option>
        {/each}
      </select>
    {/snippet}
  </Field>
  <Field label="Network" error={errors.fields.network} hint="Optional. A private network the load balancer joins.">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} bind:value={network}>
        <option value="">No network</option>
        {#each networks as n (n.id)}<option value={`${n.id}`}>{n.name} ({n.ip_range})</option>{/each}
      </select>
    {/snippet}
  </Field>
  <label class="check single"><input type="checkbox" bind:checked={publicInterface} />Public interface</label>

  <fieldset>
    <legend>Targets</legend>
    {#each targets as _, i (i)}
      <div class="item">
        <TargetEditor bind:target={targets[i]} {servers} />
        <Button variant="icon" label={`Remove target ${i + 1}`} onclick={() => (targets = targets.filter((_, j) => j !== i))}>
          <X size={16} aria-hidden="true" />
        </Button>
      </div>
    {/each}
    {#if errors.fields.targets}<div class="error">{errors.fields.targets}</div>{/if}
    <div><Button onclick={() => (targets = [...targets, newTarget()])}><Plus size={16} aria-hidden="true" />Add target</Button></div>
  </fieldset>

  <fieldset>
    <legend>Services</legend>
    {#each services as _, i (i)}
      <div class="item">
        <ServiceEditor bind:service={services[i]} {certificates} />
        <Button variant="icon" label={`Remove service ${i + 1}`} onclick={() => (services = services.filter((_, j) => j !== i))}>
          <X size={16} aria-hidden="true" />
        </Button>
      </div>
    {/each}
    {#if errors.fields.services}<div class="error">{errors.fields.services}</div>{/if}
    <div><Button onclick={() => (services = [...services, newService()])}><Plus size={16} aria-hidden="true" />Add service</Button></div>
  </fieldset>

  <fieldset>
    <legend>Algorithm</legend>
    <label class="check"><input type="radio" name="algorithm" value="round_robin" bind:group={algorithm} />Round robin</label>
    <label class="check"><input type="radio" name="algorithm" value="least_connections" bind:group={algorithm} />Least connections</label>
    {#if errors.fields.algorithm}<div class="error">{errors.fields.algorithm}</div>{/if}
  </fieldset>

  <LabelsField bind:rows={labelRows} error={errors.fields.labels} />
  <Field label="Name" error={errors.fields.name}>
    {#snippet children(c)}
      <input id={c.id} aria-describedby={c.describedby} aria-invalid={c.invalid} autocomplete="off" bind:value={name} />
    {/snippet}
  </Field>
</FormPage>

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
  .item {
    display: flex;
    align-items: flex-start;
    gap: var(--s2);
    padding: var(--s3);
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  .item > :global(:first-child) {
    flex: 1;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .single {
    margin-bottom: var(--s4);
  }
  select,
  input:not([type="radio"]):not([type="checkbox"]) {
    width: 100%;
  }
  .error {
    color: var(--error);
    font-size: 12px;
    line-height: 16px;
  }
</style>
