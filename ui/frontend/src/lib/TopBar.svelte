<script lang="ts">
  import { Play, Settings, Square } from "@lucide/svelte";
  import type { Snippet } from "svelte";
  import Button from "./Button.svelte";
  import StatusPill from "./StatusPill.svelte";
  import { faultsPill } from "./faults";
  import { showError } from "./toast";
  import appIcon from "../assets/app-icon.svg";
  import { adminReturn, cloud, href, route, startCloud, stopCloud, type CloudState } from "../state";

  // Top bar on every page: "HLab" (to the project list), the breadcrumb of
  // the area, while faults are active a pill with their number (to Active
  // faults), the cloud status pill, the Start/Stop button and the gear,
  // which opens the Administration and, while it is open, returns to the
  // route shown before it.
  let { breadcrumb }: { breadcrumb?: Snippet } = $props();

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
  <a class="home" href={href({ area: "projects" })}><img src={appIcon} alt="" width="24" height="24" />HLab</a>
  <div class="breadcrumb">{@render breadcrumb?.()}</div>
  {#if $cloud.state === "running" && $cloud.faults > 0}
    <a class="faults" href={href({ area: "faults" })}><StatusPill tone="warning" text={faultsPill($cloud.faults)} /></a>
  {/if}
  <StatusPill role="status" tone={pill.tone} text={pill.text} />
  {#if canStop}
    <Button loading={sending} onclick={() => run("Stop cloud", stopCloud)}><Square size={16} aria-hidden="true" />Stop cloud</Button>
  {:else}
    <Button variant="primary" loading={sending || busy} disabled={!canStart} onclick={() => run("Start cloud", () => startCloud())}>
      <Play size={16} aria-hidden="true" />Start cloud
    </Button>
  {/if}
  <a
    class="gear"
    href={$route.area === "admin" ? $adminReturn : href({ area: "admin", view: "server-types" })}
    aria-label="Administration"
    title="Administration"
    aria-current={$route.area === "admin" ? "page" : undefined}><Settings size={20} aria-hidden="true" /></a
  >
</header>

<style>
  .topbar {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s2) var(--s6);
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
  .home {
    display: flex;
    align-items: center;
    gap: var(--s2);
    color: var(--text);
    font-size: 16px;
    font-weight: 600;
    text-decoration: none;
  }
  .breadcrumb {
    flex: 1;
    min-width: 0;
  }
  .faults {
    color: var(--text);
    text-decoration: none;
  }
  .faults :global(.pill) {
    border-color: var(--warning);
  }
  .gear {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border-radius: var(--radius);
    color: var(--text);
  }
  .gear:hover,
  .gear[aria-current="page"] {
    background: var(--bg);
  }
  .gear[aria-current="page"] {
    color: var(--accent);
  }
</style>
