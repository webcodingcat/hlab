<script lang="ts">
  import { Play, Square } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import StatusPill from "./StatusPill.svelte";
  import { showError } from "./toast";
  import { cloud, startCloud, stopCloud, type CloudState } from "../state";

  // Top bar: the cloud status pill and the Start/Stop button.
  type Pill = { tone: "success" | "warning" | "muted" | "error"; text: string };

  function pillFor(c: CloudState): Pill {
    switch (c.state) {
      case "running":
        return c.connection_lost ? { tone: "error", text: "Running, connection lost" } : { tone: "success", text: "Running" };
      case "starting":
        return { tone: "warning", text: "Starting" };
      case "stopping":
        return { tone: "warning", text: "Stopping" };
      case "refused":
        return { tone: "error", text: "Start refused" };
      case "stopped":
        return c.error ? { tone: "error", text: "Stopped, start failed" } : { tone: "muted", text: "Stopped" };
      default:
        return { tone: "muted", text: "Checking" };
    }
  }

  const pill = $derived(pillFor($cloud));
  const canStart = $derived($cloud.state === "stopped" || $cloud.state === "refused");
  const canStop = $derived($cloud.state === "running");
  const busy = $derived($cloud.state === "starting" || $cloud.state === "stopping");
  let sending = $state(false);

  async function run(title: string, fn: () => Promise<void>) {
    sending = true;
    try {
      await fn();
    } catch (err) {
      showError(title, err);
    } finally {
      sending = false;
    }
  }
</script>

<header class="topbar">
  <StatusPill role="status" tone={pill.tone} text={pill.text} />
  {#if canStop}
    <Button loading={sending} onclick={() => run("Stop cloud", stopCloud)}><Square size={16} aria-hidden="true" />Stop cloud</Button>
  {:else}
    <Button variant="primary" loading={sending || busy} disabled={!canStart} onclick={() => run("Start cloud", () => startCloud())}>
      <Play size={16} aria-hidden="true" />Start cloud
    </Button>
  {/if}
</header>

<style>
  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    padding: var(--s2) var(--s6);
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
</style>
