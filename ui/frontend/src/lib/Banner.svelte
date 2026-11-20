<script lang="ts">
  import { CircleAlert } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import ConfirmDialog from "./ConfirmDialog.svelte";
  import { showError } from "./toast";
  import { cloud, startCloud } from "../state";

  // Banner on top of the content: connection to the service lost, a start
  // refused for an incompatible state, or a failed start.
  let confirming = $state(false);

  async function discardAndStart() {
    try {
      await startCloud(true);
    } catch (err) {
      showError("Discard state and start", err);
    }
  }
</script>

{#if $cloud.state === "running" && $cloud.connection_lost}
  <div class="banner" role="alert">
    <CircleAlert size={16} aria-hidden="true" />
    <span>Connection to the cloud service lost. Retrying.</span>
  </div>
{:else if $cloud.state === "refused" && $cloud.refused}
  <div class="banner" role="alert">
    <CircleAlert size={16} aria-hidden="true" />
    <span class="text">The cloud refused to start: {$cloud.refused.message}</span>
    <Button variant="danger" onclick={() => (confirming = true)}>Discard state and start</Button>
  </div>
{:else if $cloud.state === "stopped" && $cloud.error}
  <div class="banner" role="alert">
    <CircleAlert size={16} aria-hidden="true" />
    <span>The last cloud start failed: {$cloud.error}</span>
  </div>
{/if}

<ConfirmDialog bind:open={confirming} title="Discard state and start" confirmLabel="Discard state and start" onconfirm={discardAndStart}>
  <p>The cloud deletes its servers, disks, volumes and images, sets the stored state aside and starts with an empty state.</p>
</ConfirmDialog>

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: var(--s2);
    padding: var(--s2) var(--s6);
    border-bottom: 1px solid var(--error);
    background: var(--surface);
    color: var(--error);
  }
  .text {
    flex: 1;
  }
</style>
