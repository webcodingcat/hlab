<script lang="ts">
  import { untrack } from "svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import { api } from "../../lib/api";
  import { createdText } from "../../lib/format";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import type { Iso } from "./Section.svelte";

  // The detail page of one ISO: Overview only, read-only; ISOs are attached
  // in a server's ISO tab.
  let { project, id, list }: { project: string; id: number; list: string } = $props();

  const tabs = $derived([
    { id: "overview", label: "Overview", href: href({ area: "project", project, view: "isos", page: { kind: "detail", id, tab: "overview" } }) },
  ]);

  let iso = $state<Iso | undefined>();
  let notFound = $state(false);

  $effect(() => {
    const path = `/isos/${id}`;
    untrack(() => {
      iso = undefined;
      notFound = false;
    });
    api<{ iso: Iso }>(project, "GET", path)
      .then((r) => (iso = r.iso))
      .catch((err) => {
        if ((err as { code?: unknown }).code === "not_found") notFound = true;
        else showError("Load ISO", err);
      });
  });
</script>

{#if notFound}
  <DetailPage title="ISO" help="isos" {tabs} current="overview" back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !iso}
  <p class="muted" aria-busy="true">Loading the ISO…</p>
{:else}
  {@const i = iso}
  <DetailPage title={i.name} help="isos" {tabs} current="overview" back={list}>
    <dl>
      <dt>Description</dt>
      <dd>{i.description}</dd>
      <dt>Type</dt>
      <dd>{i.type}</dd>
      <dt>Architecture</dt>
      <dd>{i.architecture ?? "any"}</dd>
      <dt>Deprecated</dt>
      <dd>
        {#if i.deprecation}
          Since {createdText(i.deprecation.announced)}, unavailable after {createdText(i.deprecation.unavailable_after)}
        {:else}
          No
        {/if}
      </dd>
    </dl>
    <p class="muted note">To use this ISO, attach it in the ISO tab of a server's page.</p>
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
  .muted {
    color: var(--muted);
  }
  .note {
    margin-top: var(--s4);
  }
</style>
