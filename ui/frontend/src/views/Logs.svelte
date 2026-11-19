<script lang="ts">
  import HelpButton from "../lib/HelpButton.svelte";
  import { on, service } from "../lib/bridge";
  import { showError } from "../lib/toast";

  // hlabd's log as the tray's Logs service sends it: the last 1000 lines at
  // a follow's start, then new ones in `logs` events, from the stream while
  // the cloud runs and from the files while it is stopped.

  type Line = { id: number; time: string; level: string; msg: string; fields: string };

  const keep = 5000;
  const levels = ["DEBUG", "INFO", "WARN", "ERROR"];

  let lines = $state.raw<Line[]>([]);
  let follow = $state(true);
  let minLevel = $state("");
  let list = $state<HTMLElement | undefined>();
  let nextId = 0;

  const shown = $derived(minLevel === "" ? lines : lines.filter((l) => rank(l.level) >= rank(minLevel)));

  // rank orders levels; a line without a known level is always shown.
  function rank(level: string): number {
    const i = levels.indexOf(level.toUpperCase());
    return i < 0 ? levels.length : i;
  }

  function pad(n: number, width = 2): string {
    return String(n).padStart(width, "0");
  }

  function formatTime(t: unknown): string {
    if (typeof t !== "string") return "";
    const d = new Date(t);
    if (Number.isNaN(d.getTime())) return t;
    return (
      `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
      `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
    );
  }

  function formatValue(v: unknown): string {
    if (typeof v === "string") return /[\s"=]/.test(v) || v === "" ? JSON.stringify(v) : v;
    return JSON.stringify(v);
  }

  // parse reads one JSON line (time, level, msg, fields); anything else is
  // shown as it is.
  function parse(raw: string): Line {
    const id = nextId++;
    let obj: unknown;
    try {
      obj = JSON.parse(raw);
    } catch {
      return { id, time: "", level: "", msg: raw, fields: "" };
    }
    if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
      return { id, time: "", level: "", msg: raw, fields: "" };
    }
    const { time, level, msg, ...rest } = obj as Record<string, unknown>;
    const fields = Object.entries(rest)
      .map(([k, v]) => `${k}=${formatValue(v)}`)
      .join(" ");
    return {
      id,
      time: formatTime(time),
      level: typeof level === "string" ? level : "",
      msg: typeof msg === "string" ? msg : msg === undefined ? "" : JSON.stringify(msg),
      fields,
    };
  }

  function add(batch: string[]) {
    const next = lines.concat(batch.map(parse));
    lines = next.length > keep ? next.slice(next.length - keep) : next;
  }

  function setFollow(onOff: boolean) {
    if (onOff) lines = [];
    service<void>("Logs", "Follow", onOff).catch((err) => showError("Follow the log", err));
  }

  $effect(() => {
    const off = on<string[]>("logs", add);
    setFollow(true);
    return () => {
      off();
      service<void>("Logs", "Follow", false).catch(() => {});
    };
  });

  // While following, the list stays scrolled to its end.
  $effect(() => {
    void shown;
    if (follow && list) list.scrollTop = list.scrollHeight;
  });

  function levelClass(level: string): string {
    const l = level.toUpperCase();
    return l === "ERROR" ? "error" : l === "WARN" ? "warn" : l === "DEBUG" ? "debug" : "";
  }
</script>

<div class="title-row">
  <h1>Logs</h1>
  <HelpButton view="logs" />
</div>

<div class="controls">
  <label>
    Level
    <select bind:value={minLevel}>
      <option value="">All</option>
      <option value="DEBUG">Debug and above</option>
      <option value="INFO">Info and above</option>
      <option value="WARN">Warn and above</option>
      <option value="ERROR">Error</option>
    </select>
  </label>
  <label>
    <input
      type="checkbox"
      checked={follow}
      onchange={(e) => {
        follow = e.currentTarget.checked;
        setFollow(follow);
      }}
    />
    Follow
  </label>
  <span class="count">{shown.length} of {lines.length} lines</span>
</div>

<div class="log" role="log" aria-label="Service log" bind:this={list}>
  {#each shown as line (line.id)}
    <div class="line">
      {#if line.time}<span class="time">{line.time}</span>{/if}
      {#if line.level}<span class="level {levelClass(line.level)}">{line.level}</span>{/if}
      <span class="msg">{line.msg}</span>
      {#if line.fields}<span class="fields">{line.fields}</span>{/if}
    </div>
  {/each}
</div>

<style>
  .title-row {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s4);
  }
  .controls {
    display: flex;
    align-items: center;
    gap: var(--s4);
    margin-bottom: var(--s3);
  }
  .controls label {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .count {
    color: var(--muted);
    margin-left: auto;
  }
  .log {
    height: calc(100vh - 220px);
    min-height: 200px;
    overflow: auto;
    padding: var(--s2);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.5;
  }
  .line {
    white-space: pre-wrap;
    word-break: break-word;
  }
  .line > span + span {
    margin-left: var(--s2);
  }
  .time,
  .fields {
    color: var(--muted);
  }
  .level {
    display: inline-block;
    min-width: 5ch;
    font-weight: 600;
  }
  .level.error {
    color: var(--error);
  }
  .level.warn {
    color: var(--warning);
  }
  .level.debug {
    color: var(--muted);
  }
</style>
