<script lang="ts">
  import { Globe, HardDrive, KeyRound, MapPin, Network, Scale, Server, Shield } from "@lucide/svelte";
  import type { Component } from "svelte";
  import SectionSidebar, { type SectionGroup } from "../lib/SectionSidebar.svelte";
  import SectionTabs from "../lib/SectionTabs.svelte";
  import { entryOf, navGroups } from "../lib/consoleNav";
  import { href, type Page, type ProjectView } from "../state";
  import Tokens from "./Tokens.svelte";
  import Servers from "./servers/Servers.svelte";
  import SshKeys from "./sshkeys/SshKeys.svelte";

  // The console of one project: the sidebar with this project's entries in
  // their groups, the tab bar of the open entry and the open view. The
  // resource sections live in ./<view>/Section.svelte.
  let { project, view, page }: { project: string; view: ProjectView; page: Page } = $props();

  type SectionProps = { project: string; page: Page };
  const found = import.meta.glob<{ default: Component<SectionProps> }>("./*/Section.svelte", { eager: true });
  const sectionComponents: Partial<Record<ProjectView, Component<SectionProps>>> = Object.fromEntries(
    Object.entries(found).map(([path, m]) => [path.split("/")[1], m.default]),
  );

  const icons: Record<string, typeof Server> = {
    servers: Server,
    volumes: HardDrive,
    firewalls: Shield,
    "load-balancers": Scale,
    networks: Network,
    ips: MapPin,
    dns: Globe,
    security: KeyRound,
  };

  const Section = $derived(sectionComponents[view]);
  const open = $derived(entryOf(view));

  const groups: SectionGroup[] = $derived(
    navGroups.map((g) => ({
      id: g.id,
      title: g.title,
      items: g.entries.map((e) => ({
        href: href({ area: "project", project, view: e.tabs[0].view, page: { kind: "list" } }),
        title: e.title,
        icon: icons[e.id],
        current: e.id === open.id,
      })),
    })),
  );
</script>

<div class="area">
  <SectionSidebar label="Project" {groups} />
  <main>
    <SectionTabs {project} {view} />
    {#if view === "servers"}<Servers {project} {page} />
    {:else if view === "ssh-keys"}<SshKeys {project} {page} />
    {:else if view === "tokens"}<Tokens name={project} />
    {:else if Section}<Section {project} {page} />{/if}
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
