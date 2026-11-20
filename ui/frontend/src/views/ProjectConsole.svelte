<script lang="ts">
  import { Key, KeyRound, Server } from "@lucide/svelte";
  import SectionSidebar from "../lib/SectionSidebar.svelte";
  import { href, type Page, type ProjectView } from "../state";
  import Tokens from "./Tokens.svelte";
  import Servers from "./servers/Servers.svelte";
  import SshKeys from "./sshkeys/SshKeys.svelte";

  // The console of one project: the sidebar with this project's sections
  // and the open section. "API tokens" follows the resource sections after
  // a separator.
  let { project, view, page }: { project: string; view: ProjectView; page: Page } = $props();

  const sections: { view: ProjectView; title: string; icon: typeof Server; separator?: boolean }[] = [
    { view: "servers", title: "Servers", icon: Server },
    { view: "ssh-keys", title: "SSH Keys", icon: Key },
    { view: "tokens", title: "API tokens", icon: KeyRound, separator: true },
  ];

  const items = $derived(
    sections.map((s) => ({
      href: href({ area: "project", project, view: s.view, page: { kind: "list" } }),
      title: s.title,
      icon: s.icon,
      current: s.view === view,
      separator: s.separator,
    })),
  );
</script>

<div class="area">
  <SectionSidebar label="Project" {items} />
  <main>
    {#if view === "servers"}<Servers {project} {page} />
    {:else if view === "ssh-keys"}<SshKeys {project} {page} />
    {:else}<Tokens name={project} />{/if}
  </main>
</div>

<style>
  .area {
    display: flex;
    flex: 1;
  }
  main {
    width: 100%;
    max-width: 1200px;
    min-width: 0;
    padding: var(--s6);
  }
</style>
