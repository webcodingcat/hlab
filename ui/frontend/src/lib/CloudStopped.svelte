<script lang="ts">
  import { Play } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import { showError } from "./toast";
  import { cloud, startCloud } from "../state";

  // What the project list and the project console show while the cloud
  // does not run: one sentence and the Start button.
  const canStart = $derived($cloud.state === "stopped" || $cloud.state === "refused");
  const busy = $derived($cloud.state === "starting" || $cloud.state === "stopping");
  let sending = $state(false);

  async function start() {
    sending = true;
    try {
      await startCloud();
    } catch (err) {
      showError("Start cloud", err);
    } finally {
      sending = false;
    }
  }
</script>

<div class="stopped">
  <p>Start the cloud to manage projects</p>
  <Button variant="primary" loading={sending || busy} disabled={!canStart} onclick={start}>
    <Play size={16} aria-hidden="true" />Start cloud
  </Button>
</div>

<style>
  .stopped {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--s4);
    padding: var(--s8);
    text-align: center;
    background: var(--surface);
    border: 1px solid var(--border);
  }
</style>
