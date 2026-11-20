<script lang="ts">
  import CloudStopped from "../../lib/CloudStopped.svelte";
  import HelpButton from "../../lib/HelpButton.svelte";
  import { running, type Page } from "../../state";

  // The "SSH Keys" section of a project console; page says which of its
  // pages the route opens (list, add form or detail).
  let { project, page }: { project: string; page: Page } = $props();
</script>

<div class="title-row">
  <h1>SSH Keys</h1>
  <HelpButton view="ssh-keys" />
</div>

{#if !$running}
  <CloudStopped />
{/if}

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
</style>
