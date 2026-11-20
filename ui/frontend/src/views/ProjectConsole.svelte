<script lang="ts">
  import {
    Archive,
    Book,
    Disc,
    Globe,
    Group,
    HardDrive,
    Images,
    Key,
    KeyRound,
    MapPin,
    Network,
    Scale,
    Server,
    Shield,
    ShieldCheck,
  } from "@lucide/svelte";
  import type { Component } from "svelte";
  import SectionSidebar from "../lib/SectionSidebar.svelte";
  import { href, type Page, type ProjectView } from "../state";
  import Tokens from "./Tokens.svelte";
  import Servers from "./servers/Servers.svelte";
  import SshKeys from "./sshkeys/SshKeys.svelte";

  // The console of one project: the sidebar with this project's sections
  // and the open section. "API tokens" follows the resource sections after
  // a separator. The newer resource sections live in ./<view>/Section.svelte
  // and are listed only once that component exists.
  let { project, view, page }: { project: string; view: ProjectView; page: Page } = $props();

  type SectionProps = { project: string; page: Page };
  const found = import.meta.glob<{ default: Component<SectionProps> }>("./*/Section.svelte", { eager: true });
  const sectionComponents: Partial<Record<ProjectView, Component<SectionProps>>> = Object.fromEntries(
    Object.entries(found).map(([path, m]) => [path.split("/")[1], m.default]),
  );
  const builtIn: ProjectView[] = ["servers", "ssh-keys", "tokens"];

  const sections: { view: ProjectView; title: string; icon: typeof Server; separator?: boolean }[] = [
    { view: "servers", title: "Servers", icon: Server },
    { view: "volumes", title: "Volumes", icon: HardDrive },
    { view: "load-balancers", title: "Load Balancers", icon: Scale },
    { view: "networks", title: "Networks", icon: Network },
    { view: "firewalls", title: "Firewalls", icon: Shield },
    { view: "floating-ips", title: "Floating IPs", icon: Globe },
    { view: "primary-ips", title: "Primary IPs", icon: MapPin },
    { view: "dns", title: "DNS", icon: Book },
    { view: "certificates", title: "Certificates", icon: ShieldCheck },
    { view: "ssh-keys", title: "SSH Keys", icon: Key },
    { view: "placement-groups", title: "Placement Groups", icon: Group },
    { view: "snapshots", title: "Snapshots", icon: Images },
    { view: "backups", title: "Backups", icon: Archive },
    { view: "isos", title: "ISOs", icon: Disc },
    { view: "tokens", title: "API tokens", icon: KeyRound, separator: true },
  ];

  const Section = $derived(sectionComponents[view]);

  const items = $derived(
    sections
      .filter((s) => builtIn.includes(s.view) || sectionComponents[s.view])
      .map((s) => ({
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
