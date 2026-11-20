<script lang="ts">
  import { Cpu, ScrollText, Settings as SettingsIcon } from "@lucide/svelte";
  import Banner from "./lib/Banner.svelte";
  import ProjectMenu from "./lib/ProjectMenu.svelte";
  import SectionSidebar, { type SectionItem } from "./lib/SectionSidebar.svelte";
  import Toast from "./lib/Toast.svelte";
  import TopBar from "./lib/TopBar.svelte";
  import { href, initCloud, initRoute, route, views, type ViewId } from "./state";
  import Logs from "./views/Logs.svelte";
  import ProjectConsole from "./views/ProjectConsole.svelte";
  import Projects from "./views/Projects.svelte";
  import ServerTypes from "./views/ServerTypes.svelte";
  import Settings from "./views/Settings.svelte";

  $effect(() => {
    const offCloud = initCloud();
    const offRoute = initRoute();
    return () => {
      offCloud();
      offRoute();
    };
  });

  // The Administration sidebar: the admin views of views.json.
  const adminIcons: Partial<Record<ViewId, SectionItem["icon"]>> = {
    "server-types": Cpu,
    settings: SettingsIcon,
    logs: ScrollText,
  };
  const adminItems = $derived(
    views
      .filter((v) => v.area === "admin")
      .map((v) => ({
        href: href({ area: "admin", view: v.id as "server-types" | "settings" | "logs" }),
        title: v.title,
        icon: adminIcons[v.id] ?? Cpu,
        current: $route.area === "admin" && $route.view === v.id,
      })),
  );
</script>

<div class="shell">
  <TopBar>
    {#snippet breadcrumb()}
      <nav aria-label="Breadcrumb">
        {#if $route.area === "project"}
          <a href={href({ area: "projects" })}>Projects</a>
          <span aria-hidden="true">/</span>
          {#key $route.project}<ProjectMenu project={$route.project} />{/key}
        {:else if $route.area === "admin"}
          <span aria-current="page">Administration</span>
        {:else}
          <span aria-current="page">Projects</span>
        {/if}
      </nav>
    {/snippet}
  </TopBar>
  <Banner />
  {#if $route.area === "admin"}
    <div class="area">
      <SectionSidebar label="Administration" items={adminItems} />
      <main>
        {#if $route.view === "server-types"}<ServerTypes />
        {:else if $route.view === "settings"}<Settings />
        {:else}<Logs />{/if}
      </main>
    </div>
  {:else if $route.area === "project"}
    {#key $route.project}<ProjectConsole project={$route.project} />{/key}
  {:else}
    <main><Projects /></main>
  {/if}
</div>
<Toast />

<style>
  .shell {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }
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
  nav {
    display: flex;
    align-items: center;
    gap: var(--s2);
    color: var(--muted);
  }
  nav [aria-current="page"] {
    color: var(--text);
  }
</style>
