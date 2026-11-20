<script lang="ts">
  import Button from "../lib/Button.svelte";
  import HelpButton from "../lib/HelpButton.svelte";
  import { on, service } from "../lib/bridge";
  import { showError } from "../lib/toast";

  // hlabd's log as the tray's Logs service sends it: the last 1000 lines at
  // a follow's start, then new ones in `logs` events, from the stream while
  // the cloud runs and from the files while it is stopped. "Load older lines"
  // puts the lines before the oldest shown one in front; a search shows the
  // matching lines of all log files instead of the live view.

  type Line = { id: number; raw: string; time: string; level: string; msg: string; fields: string };
  type SearchResult = { lines: string[]; capped: boolean };
  type OlderResult = { lines: string[]; at_start: boolean };

  const keep = 5000;
  const searchDelay = 300;
  const levels = ["DEBUG", "INFO", "WARN", "ERROR"];

  // older holds the loaded older lines, which are never trimmed; live holds
  // the followed ones, at most keep.
  let older = $state.raw<Line[]>([]);
  let live = $state.raw<Line[]>([]);
  let follow = $state(true);
  let minLevel = $state("");
  let list = $state<HTMLElement | undefined>();
  let nextId = 0;

  // olderKnown: whether lines before the oldest shown one exist is known;
  // atStart: they do not. The first answer of Logs.Older after a follow
  // starts is kept in probe for the first click.
  let olderKnown = $state(false);
  let atStart = $state(true);
  let loadingOlder = $state(false);
  let probe: { oldest: string; result: OlderResult } | undefined;
  // generation changes when the live view restarts; answers for an earlier
  // generation are dropped.
  let generation = 0;

  let query = $state("");
  // results: the matches of the shown search, newest first; undefined in
  // the live view. searched is their query.
  let results = $state.raw<Line[] | undefined>(undefined);
  let searched = $state("");
  let capped = $state(false);
  let searching = $state(false);
  let searchSeq = 0;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  const lines = $derived(older.length === 0 ? live : older.concat(live));
  const source = $derived(results ?? lines);
  const shown = $derived(minLevel === "" ? source : source.filter((l) => rank(l.level) >= rank(minLevel)));
  const matchCount = $derived(
    results === undefined
      ? ""
      : capped
        ? `${results.length}+ matches`
        : results.length === 1
          ? "1 match"
          : `${results.length} matches`,
  );

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
      return { id, raw, time: "", level: "", msg: raw, fields: "" };
    }
    if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
      return { id, raw, time: "", level: "", msg: raw, fields: "" };
    }
    const { time, level, msg, ...rest } = obj as Record<string, unknown>;
    const fields = Object.entries(rest)
      .map(([k, v]) => `${k}=${formatValue(v)}`)
      .join(" ");
    return {
      id,
      raw,
      time: formatTime(time),
      level: typeof level === "string" ? level : "",
      msg: typeof msg === "string" ? msg : msg === undefined ? "" : JSON.stringify(msg),
      fields,
    };
  }

  function add(batch: string[]) {
    const first = lines.length === 0;
    const next = live.concat(batch.map(parse));
    if (next.length > keep) {
      live = next.slice(next.length - keep);
      // The trimmed lines are before the shown ones.
      if (older.length === 0) {
        atStart = false;
        olderKnown = true;
      }
    } else {
      live = next;
    }
    // A follow replays oldest first, so the first batch holds the oldest line.
    if (first && live.length > 0 && !olderKnown) probeOlder(live[0].raw);
  }

  // probeOlder asks for the lines before oldest, so the button is disabled
  // when the log starts within the shown lines.
  function probeOlder(oldest: string) {
    const gen = generation;
    service<OlderResult>("Logs", "Older", oldest)
      .then((result) => {
        if (gen !== generation) return;
        probe = { oldest, result };
        atStart = result.lines.length === 0;
        olderKnown = true;
      })
      .catch((err) => {
        if (gen !== generation) return;
        atStart = false;
        olderKnown = true;
        showError("Load older lines", err);
      });
  }

  async function loadOlder() {
    const oldest = lines[0].raw;
    const gen = generation;
    loadingOlder = true;
    try {
      const result =
        probe && probe.oldest === oldest ? probe.result : await service<OlderResult>("Logs", "Older", oldest);
      probe = undefined;
      if (gen !== generation) return;
      if (result.lines.length > 0) older = result.lines.map(parse).concat(older);
      atStart = result.at_start || result.lines.length === 0;
    } catch (err) {
      showError("Load older lines", err);
    } finally {
      loadingOlder = false;
    }
  }

  function setFollow(onOff: boolean) {
    if (onOff) {
      generation++;
      older = [];
      live = [];
      probe = undefined;
      olderKnown = false;
      atStart = true;
    }
    service<void>("Logs", "Follow", onOff).catch((err) => showError("Follow the log", err));
  }

  // backToLive leaves the search and follows the log again.
  function backToLive() {
    searchSeq++;
    clearTimeout(searchTimer);
    results = undefined;
    searched = "";
    capped = false;
    searching = false;
    follow = true;
    setFollow(true);
  }

  async function runSearch() {
    clearTimeout(searchTimer);
    const q = query;
    if (q === "") {
      return;
    }
    const seq = ++searchSeq;
    searching = true;
    try {
      const res = await service<SearchResult>("Logs", "Search", q, minLevel);
      if (seq !== searchSeq) return;
      if (follow) {
        follow = false;
        setFollow(false);
      }
      results = res.lines.map(parse);
      capped = res.capped;
      searched = q;
    } catch (err) {
      if (seq === searchSeq) showError("Search the log", err);
    } finally {
      if (seq === searchSeq) searching = false;
    }
  }

  function onSearchInput() {
    clearTimeout(searchTimer);
    if (query === "") {
      if (results !== undefined || searching) backToLive();
      return;
    }
    searchTimer = setTimeout(runSearch, searchDelay);
  }

  $effect(() => {
    const off = on<string[]>("logs", add);
    setFollow(true);
    return () => {
      off();
      clearTimeout(searchTimer);
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

  // parts splits text at the case-insensitive matches of q.
  function parts(text: string, q: string): { text: string; match: boolean }[] {
    if (q === "") return [{ text, match: false }];
    const lower = text.toLowerCase();
    const needle = q.toLowerCase();
    const out: { text: string; match: boolean }[] = [];
    let at = 0;
    for (let i = lower.indexOf(needle); i >= 0; i = lower.indexOf(needle, at)) {
      if (i > at) out.push({ text: text.slice(at, i), match: false });
      out.push({ text: text.slice(i, i + needle.length), match: true });
      at = i + needle.length;
    }
    if (at < text.length) out.push({ text: text.slice(at), match: false });
    return out;
  }
</script>

<!-- prettier-ignore -->
{#snippet marked(text: string)}{#each parts(text, searched) as p, i (i)}{#if p.match}<mark>{p.text}</mark>{:else}{p.text}{/if}{/each}{/snippet}

<div class="title-row">
  <h1>Logs</h1>
  <HelpButton view="logs" />
</div>

<div class="controls">
  <input
    type="search"
    aria-label="Search the log"
    placeholder="Search the log"
    bind:value={query}
    oninput={onSearchInput}
    onkeydown={(e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        void runSearch();
      }
    }}
  />
  <label>
    Level
    <select
      bind:value={minLevel}
      onchange={() => {
        if (results !== undefined) void runSearch();
      }}
    >
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
        if (e.currentTarget.checked && results !== undefined) {
          query = "";
          backToLive();
          return;
        }
        follow = e.currentTarget.checked;
        setFollow(follow);
      }}
    />
    Follow
  </label>
  {#if results !== undefined}
    <span class="count">{matchCount}</span>
  {:else}
    <span class="count">{shown.length} of {lines.length} lines</span>
  {/if}
</div>

{#if results === undefined}
  <!-- Hidden until it is known whether older lines exist. -->
  <div class="older" class:unknown={lines.length > 0 && !olderKnown}>
    <Button onclick={loadOlder} loading={loadingOlder} disabled={atStart || lines.length === 0}>
      Load older lines
    </Button>
  </div>
{/if}

<div class="log" role="log" aria-label="Service log" aria-busy={searching || undefined} bind:this={list}>
  {#each shown as line (line.id)}
    <div class="line">
      {#if line.time}<span class="time">{line.time}</span>{/if}
      {#if line.level}<span class="level {levelClass(line.level)}">{@render marked(line.level)}</span>{/if}
      <span class="msg">{@render marked(line.msg)}</span>
      {#if line.fields}<span class="fields">{@render marked(line.fields)}</span>{/if}
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
  input[type="search"] {
    width: 280px;
  }
  .count {
    color: var(--muted);
    margin-left: auto;
  }
  .older {
    margin-bottom: var(--s2);
  }
  .older.unknown {
    visibility: hidden;
  }
  .log {
    height: calc(100vh - 270px);
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
  mark {
    background: color-mix(in srgb, var(--warning) 40%, transparent);
    color: inherit;
    border-radius: 2px;
  }
</style>
