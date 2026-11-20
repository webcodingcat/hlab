<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { api, listAll } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import type { ResourceAction } from "../../lib/resourceActions";
  import { menu } from "../../lib/resources/certificates";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import { statusText, type Certificate } from "./Section.svelte";

  // The detail page of one certificate: Overview (type, status with the
  // issuance error, domains, fingerprint, validity, the load balancers that
  // use it) and Labels; the actions menu retries a failed managed
  // certificate and deletes it (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Named = { id: number; name: string };

  const path = $derived(`/certificates/${id}`);
  const tabs = $derived(
    [
      { id: "overview", label: "Overview" },
      { id: "labels", label: "Labels" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "certificates", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "overview");

  let cert = $state<Certificate | undefined>();
  let loadBalancers = $state<Named[]>([]);
  let notFound = $state(false);
  let failing = false;

  async function load() {
    try {
      const [c, l] = await Promise.all([
        api<{ certificate: Certificate }>(project, "GET", path),
        listAll<Named>(project, "/load_balancers", "load_balancers"),
      ]);
      cert = c.certificate;
      loadBalancers = l;
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load certificate", err);
      failing = true;
    }
  }

  // The open certificate refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      cert = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  // Retry is offered only for a managed certificate whose issuance failed.
  const menuActions = $derived(
    menu.filter((a) => a.id !== "retry" || (cert?.type === "managed" && cert.status?.issuance === "failed")),
  );

  const lbName = (lid: number) => loadBalancers.find((l) => l.id === lid)?.name ?? `${lid}`;
  const lbHref = (lid: number) => href({ area: "project", project, view: "load-balancers", page: { kind: "detail", id: lid, tab: "overview" } });
  const time = (t: string | null) => (t ? createdText(t) : "—");

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }
</script>

{#if notFound}
  <DetailPage title="Certificate" help="certificates" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !cert}
  <p class="muted" aria-busy="true">Loading the certificate…</p>
{:else}
  {@const c = cert}
  <DetailPage title={c.name} help="certificates" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={c.id} name={c.name} actions={menuActions} ondone={done} />
    {/snippet}
    {#if current === "labels"}
      <LabelsTab {project} {path} labels={c.labels} onsaved={load} />
    {:else}
      <dl>
        <dt>Type</dt>
        <dd>{c.type === "managed" ? "Managed (HLab CA)" : "Uploaded"}</dd>
        <dt>Status</dt>
        <dd>
          {statusText(c)}
          {#if c.status?.error}
            <div class="error" role="alert">{c.status.error.message} ({c.status.error.code})</div>
          {/if}
        </dd>
        <dt>Domains</dt>
        <dd>
          {#each c.domain_names as d (d)}<div>{d}</div>{:else}<span class="muted">None</span>{/each}
        </dd>
        <dt>Fingerprint</dt>
        <dd class="mono">{c.fingerprint ?? "—"}</dd>
        <dt>Valid from</dt>
        <dd>{time(c.not_valid_before)}</dd>
        <dt>Valid until</dt>
        <dd>{time(c.not_valid_after)}</dd>
        <dt>Used by</dt>
        <dd>
          {#each c.used_by as u (`${u.type} ${u.id}`)}
            <div>
              {#if u.type === "load_balancer"}<a href={lbHref(u.id)}>{lbName(u.id)}</a>{:else}{u.type} {u.id}{/if}
            </div>
          {:else}
            <span class="muted">Not in use</span>
          {/each}
        </dd>
        <dt>Created</dt>
        <dd>{createdText(c.created)}</dd>
      </dl>
    {/if}
  </DetailPage>
{/if}

<style>
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--s2) var(--s4);
    max-width: 640px;
    margin: 0;
  }
  dt {
    color: var(--muted);
    font-weight: 600;
  }
  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .mono {
    font-family: var(--mono);
  }
  .error {
    color: var(--error);
  }
  .muted {
    color: var(--muted);
  }
</style>
