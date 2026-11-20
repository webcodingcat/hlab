<script lang="ts">
  import { KeyRound } from "@lucide/svelte";
  import SectionSidebar from "../lib/SectionSidebar.svelte";
  import { href } from "../state";
  import Tokens from "./Tokens.svelte";

  // The console of one project: the sidebar with this project's sections
  // and the open section. Phase 10 has only "API tokens", which follows
  // the resource sections after a separator.
  let { project }: { project: string } = $props();

  const items = $derived([
    { href: href({ area: "project", project, view: "tokens" }), title: "API tokens", icon: KeyRound, current: true, separator: true },
  ]);
</script>

<div class="area">
  <SectionSidebar label="Project" {items} />
  <main>
    <Tokens name={project} />
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
