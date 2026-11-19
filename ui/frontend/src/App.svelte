<script lang="ts">
  import Banner from "./lib/Banner.svelte";
  import Sidebar from "./lib/Sidebar.svelte";
  import Toast from "./lib/Toast.svelte";
  import TopBar from "./lib/TopBar.svelte";
  import { initCloud, initView, view } from "./state";
  import Logs from "./views/Logs.svelte";
  import Projects from "./views/Projects.svelte";
  import ServerTypes from "./views/ServerTypes.svelte";
  import Settings from "./views/Settings.svelte";

  $effect(() => {
    const offCloud = initCloud();
    const offView = initView();
    return () => {
      offCloud();
      offView();
    };
  });
</script>

<div class="shell">
  <Sidebar />
  <div class="column">
    <TopBar />
    <Banner />
    <main>
      {#if $view === "projects"}<Projects />
      {:else if $view === "server-types"}<ServerTypes />
      {:else if $view === "settings"}<Settings />
      {:else}<Logs />{/if}
    </main>
  </div>
</div>
<Toast />

<style>
  .shell {
    display: flex;
    min-height: 100vh;
  }
  .column {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
  }
  main {
    width: 100%;
    max-width: 1200px;
    padding: var(--s6);
  }
</style>
