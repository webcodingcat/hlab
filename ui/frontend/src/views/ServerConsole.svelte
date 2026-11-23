<script lang="ts">
  import type RFB from "@novnc/novnc";
  import Button from "../lib/Button.svelte";
  import StatusPill from "../lib/StatusPill.svelte";
  import { api } from "../lib/api";
  import { openConsoleProxy } from "../lib/bridge";

  // The console window of one server: its screen through noVNC, scaled to
  // the window. Each connection asks for a new console URL, opens the
  // tray's loopback proxy to it and connects noVNC to that proxy.
  let { project, server }: { project: string; server: number } = $props();

  type Console = { wss_url: string; password: string };

  let phase = $state<"connecting" | "connected" | "disconnected">("connecting");
  let reason = $state("");
  let screen = $state<HTMLDivElement>();
  let rfb: RFB | undefined;
  // attempt numbers the connections; events of an older one are ignored.
  let attempt = 0;

  const pill = $derived(
    phase === "connected"
      ? { tone: "success" as const, text: "Connected" }
      : phase === "connecting"
        ? { tone: "warning" as const, text: "Connecting" }
        : { tone: "error" as const, text: "Disconnected" },
  );

  async function connect(target: HTMLDivElement) {
    const mine = ++attempt;
    rfb?.disconnect();
    rfb = undefined;
    phase = "connecting";
    reason = "";
    try {
      const c = await api<Console>(project, "POST", `/servers/${server}/actions/request_console`);
      const url = await openConsoleProxy(c.wss_url);
      // noVNC is loaded only here, so its top-level await stays out of the
      // main window's start (E22).
      const { default: RFBClient } = await import("@novnc/novnc");
      if (mine !== attempt) return;
      // The socket is opened here so its close reason (the proxy's
      // "console: <error>") can be shown; noVNC does not pass it on.
      const ws = new WebSocket(url);
      ws.addEventListener("close", (e) => {
        if (mine === attempt && e.reason) reason = e.reason;
      });
      const r = new RFBClient(target, ws, { credentials: { password: c.password } });
      r.scaleViewport = true;
      r.addEventListener("connect", () => {
        if (mine === attempt) phase = "connected";
      });
      r.addEventListener("securityfailure", (e) => {
        if (mine === attempt) reason = (e as CustomEvent<{ reason?: string }>).detail.reason ?? "authentication failed";
      });
      r.addEventListener("disconnect", () => {
        if (mine !== attempt) return;
        phase = "disconnected";
        rfb = undefined;
      });
      rfb = r;
    } catch (err) {
      if (mine !== attempt) return;
      phase = "disconnected";
      reason = err instanceof Error ? err.message : String(err);
    }
  }

  $effect(() => {
    if (!screen) return;
    connect(screen);
    return () => {
      attempt++;
      rfb?.disconnect();
      rfb = undefined;
    };
  });
</script>

<div class="console">
  <header>
    <StatusPill tone={pill.tone} text={pill.text} />
    {#if phase === "disconnected" && reason}<span class="reason">{reason}</span>{/if}
    <span class="spacer"></span>
    {#if phase === "disconnected"}
      <Button variant="primary" onclick={() => screen && connect(screen)}>Reconnect</Button>
    {/if}
    <Button disabled={phase !== "connected"} onclick={() => rfb?.sendCtrlAltDel()}>Send Ctrl+Alt+Del</Button>
  </header>
  <div class="screen" bind:this={screen}></div>
</div>

<style>
  .console {
    display: flex;
    flex-direction: column;
    height: 100vh;
  }
  header {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s2) var(--s4);
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
  .reason {
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer {
    flex: 1;
  }
  .screen {
    flex: 1;
    min-height: 0;
    background: #000;
  }
</style>
