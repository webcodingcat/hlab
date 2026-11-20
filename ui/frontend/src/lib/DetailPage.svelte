<script lang="ts">
  import type { Snippet } from "svelte";
  import HelpButton from "./HelpButton.svelte";
  import StatusPill from "./StatusPill.svelte";
  import type { ViewId } from "../help";

  // The detail page of one resource: title row with status pill, actions
  // and help button, tabs as links (the current one has aria-current), and
  // the current tab's content. notFound shows a note with a link back to
  // the list instead.
  type Props = {
    title: string;
    status?: { tone: "success" | "warning" | "muted" | "error"; text: string };
    help: ViewId;
    tabs: { id: string; label: string; href: string }[];
    current: string;
    actions?: Snippet;
    children: Snippet;
    notFound?: boolean;
    back: string;
  };

  let { title, status, help, tabs, current, actions, children, notFound = false, back }: Props = $props();
</script>

{#if notFound}
  <div class="title-row">
    <h1>{title}</h1>
    <HelpButton view={help} />
  </div>
  <div class="not-found">
    <p>This resource was not found. It may have been deleted.</p>
    <a href={back}>Back to the list</a>
  </div>
{:else}
  <div class="title-row">
    <h1>{title}</h1>
    {#if status}<StatusPill tone={status.tone} text={status.text} />{/if}
    <span class="spacer"></span>
    {@render actions?.()}
    <HelpButton view={help} />
  </div>
  <nav aria-label="Tabs">
    {#each tabs as t (t.id)}
      <a href={t.href} aria-current={t.id === current ? "page" : undefined}>{t.label}</a>
    {/each}
  </nav>
  <section class="content">
    {@render children()}
  </section>
{/if}

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  h1 {
    overflow-wrap: anywhere;
  }
  .spacer {
    flex: 1;
  }
  nav {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s1);
    margin-bottom: var(--s4);
    border-bottom: 1px solid var(--border);
  }
  nav a {
    padding: var(--s2) var(--s3);
    margin-bottom: -1px;
    border-bottom: 2px solid transparent;
    color: var(--muted);
    text-decoration: none;
  }
  nav a:hover {
    color: var(--text);
  }
  nav a:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  nav a[aria-current="page"] {
    border-bottom-color: var(--accent);
    color: var(--text);
    font-weight: 600;
  }
  .not-found {
    padding: var(--s8);
    text-align: center;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .not-found a {
    color: var(--accent);
  }
</style>
