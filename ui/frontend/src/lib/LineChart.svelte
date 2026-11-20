<script lang="ts" module>
  export type ChartSeries = { name: string; color: string; points: [number, number][] };
</script>

<script lang="ts">
  // An SVG line chart of one or more series over a time range, with a time
  // axis, a value axis, a legend and the title as its accessible name.
  // Points are [unix seconds, value]; a gap of more than two steps breaks
  // the line.
  type Props = {
    title: string;
    series: ChartSeries[];
    start: number; // unix seconds
    end: number; // unix seconds
    step: number; // seconds between points
    format: (v: number) => string;
  };

  let { title, series, start, end, step, format }: Props = $props();

  const uid = $props.id();
  const W = 640;
  const H = 200;
  const left = 72;
  const right = 12;
  const top = 8;
  const bottom = 24;
  const plotW = W - left - right;
  const plotH = H - top - bottom;

  // niceMax rounds the largest value up to 1, 2 or 5 times a power of ten.
  function niceMax(v: number) {
    if (!(v > 0)) return 1;
    const exp = 10 ** Math.floor(Math.log10(v));
    for (const m of [1, 2, 5, 10]) if (m * exp >= v) return m * exp;
    return 10 * exp;
  }

  const empty = $derived(series.every((s) => s.points.length === 0));
  const max = $derived(niceMax(Math.max(0, ...series.flatMap((s) => s.points.map((p) => p[1])))));
  const span = $derived(Math.max(end - start, 1));
  const x = (t: number) => left + ((t - start) / span) * plotW;
  const y = (v: number) => top + plotH - (v / max) * plotH;

  const yTicks = $derived([0, 1, 2, 3, 4].map((i) => (max * i) / 4));
  const xTicks = $derived([0, 1, 2, 3, 4].map((i) => start + (span * i) / 4));
  const timeFormat = $derived<Intl.DateTimeFormatOptions>(
    span > 86400 * 1.5 ? { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" } : { hour: "2-digit", minute: "2-digit" },
  );

  function path(points: [number, number][]) {
    let d = "";
    let prev: number | undefined;
    for (const [t, v] of points) {
      const gap = prev === undefined || t - prev > 2 * step;
      d += `${gap ? "M" : "L"}${x(t).toFixed(1)},${y(v).toFixed(1)}`;
      prev = t;
    }
    return d;
  }
</script>

<figure>
  <figcaption id="{uid}-title">{title}</figcaption>
  <svg viewBox="0 0 {W} {H}" role="img" aria-labelledby="{uid}-title">
    <title>{title}</title>
    {#each yTicks as v (v)}
      <line class="grid" x1={left} x2={W - right} y1={y(v)} y2={y(v)} />
      <text class="tick" x={left - 6} y={y(v)} text-anchor="end" dominant-baseline="middle">{format(v)}</text>
    {/each}
    {#each xTicks as t, i (i)}
      <text class="tick" x={x(t)} y={H - 6} text-anchor={i === 0 ? "start" : i === 4 ? "end" : "middle"}>
        {new Date(t * 1000).toLocaleString(undefined, timeFormat)}
      </text>
    {/each}
    {#if empty}
      <text class="tick" x={left + plotW / 2} y={top + plotH / 2} text-anchor="middle">No data in this time range.</text>
    {/if}
    {#each series as s (s.name)}
      <path d={path(s.points)} fill="none" stroke={s.color} stroke-width="1.5" />
    {/each}
  </svg>
  {#if series.length > 1}
    <ul class="legend">
      {#each series as s (s.name)}
        <li><span class="swatch" style:background={s.color}></span>{s.name}</li>
      {/each}
    </ul>
  {/if}
</figure>

<style>
  figure {
    margin: 0 0 var(--s6);
    max-width: 720px;
  }
  figcaption {
    font-weight: 600;
    margin-bottom: var(--s2);
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .grid {
    stroke: var(--border);
  }
  .tick {
    fill: var(--muted);
    font-size: 11px;
  }
  .legend {
    display: flex;
    gap: var(--s4);
    list-style: none;
    margin: var(--s2) 0 0;
    padding: 0;
    color: var(--muted);
    font-size: 12px;
  }
  .legend li {
    display: flex;
    align-items: center;
    gap: var(--s1);
  }
  .swatch {
    display: inline-block;
    width: 12px;
    height: 3px;
  }
</style>
