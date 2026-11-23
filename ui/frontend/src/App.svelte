<script lang="ts">
  import { Archive, Cpu, Disc, ScrollText, Settings as SettingsIcon } from "@lucide/svelte";
  import Banner from "./lib/Banner.svelte";
  import ProjectMenu from "./lib/ProjectMenu.svelte";
  import SectionSidebar, { type SectionItem } from "./lib/SectionSidebar.svelte";
  import Toast from "./lib/Toast.svelte";
  import TopBar from "./lib/TopBar.svelte";
  import { href, initCloud, initRoute, route, views, type AdminView, type ViewId } from "./state";
  import Isos from "./views/Isos.svelte";
  import Logs from "./views/Logs.svelte";
  import ProjectConsole from "./views/ProjectConsole.svelte";
  import Projects from "./views/Projects.svelte";
  import ServerTypes from "./views/ServerTypes.svelte";
  import Settings from "./views/Settings.svelte";
  import StorageBoxTypes from "./views/StorageBoxTypes.svelte";

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
    "storage-box-types": Archive,
    isos: Disc,
    settings: SettingsIcon,
    logs: ScrollText,
  };
  const adminItems = $derived(
    views
      .filter((v) => v.area === "admin")
      .map((v) => ({
        href: href({ area: "admin", view: v.id as AdminView }),
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
  <div class="scroll">
    {#if $route.area === "admin"}
      <div class="area">
        <SectionSidebar label="Administration" items={adminItems} />
        <main>
          {#if $route.view === "server-types"}<ServerTypes />
          {:else if $route.view === "storage-box-types"}<StorageBoxTypes />
          {:else if $route.view === "isos"}<Isos />
          {:else if $route.view === "settings"}<Settings />
          {:else}<Logs />{/if}
        </main>
      </div>
    {:else if $route.area === "project"}
      {#key $route.project}<ProjectConsole project={$route.project} view={$route.view} page={$route.page} />{/key}
    {:else}
      <main><Projects /></main>
    {/if}
  </div>
</div>
<Toast />

<style>
  /* The top bar stays in place; only the area below it scrolls, and its
     scrollbar space is always reserved so nothing shifts sideways. */
  .shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
  }
  .scroll {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    overflow-y: auto;
    scrollbar-gutter: stable;
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
