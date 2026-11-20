<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import LineChart, { type ChartSeries } from "../../../lib/LineChart.svelte";
  import { api } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";

  // The Graphs tab: CPU, disk (IOPS, bandwidth) and network (PPS,
  // bandwidth) of the server for the last hour, day or week, reloaded every
  // 30 s while the window is visible.
  type Metrics = { start: string; end: string; step: number; time_series: Record<string, { values: [number, string][] }> };
  type Props = { project: string; server: Server };

  let { project, server }: Props = $props();

  const ranges = [
    { id: "1h", label: "1 hour", seconds: 3600 },
    { id: "24h", label: "24 hours", seconds: 86400 },
    { id: "7d", label: "7 days", seconds: 7 * 86400 },
  ];
  let rangeId = $state("1h");
  const range = $derived(ranges.find((r) => r.id === rangeId) ?? ranges[0]);
  let metrics = $state<Metrics | undefined>();
  let failing = false;

  const rfc3339 = (ms: number) => new Date(ms).toISOString().replace(/\.\d+Z$/, "Z");

  async function load(id: number, seconds: number) {
    const end = Date.now();
    const q = new URLSearchParams({ type: "cpu,disk,network", start: rfc3339(end - seconds * 1000), end: rfc3339(end) });
    try {
      metrics = (await api<{ metrics: Metrics }>(project, "GET", `/servers/${id}/metrics?${q}`)).metrics;
      failing = false;
    } catch (err) {
      if (!failing) showError("Load metrics", err);
      failing = true;
    }
  }

  $effect(() => {
    const id = server.id;
    const seconds = range.seconds;
    metrics = undefined;
    load(id, seconds);
    const t = setInterval(() => {
      if (!document.hidden) load(id, seconds);
    }, 30000);
    return () => clearInterval(t);
  });

  const unit = (u: string, k: number, names: string[]) => (v: number) => {
    let i = 0;
    while (v >= k && i < names.length - 1) {
      v /= k;
      i++;
    }
    return `${Number(v.toFixed(v < 10 && i > 0 ? 1 : 0))} ${names[i]}${u}`;
  };
  const percent = (v: number) => `${Number(v.toFixed(1))} %`;
  const rate = unit("/s", 1000, ["", "k", "M", "G"]);
  const bytes = unit("/s", 1024, ["B", "KiB", "MiB", "GiB"]);

  const colors = ["var(--accent)", "var(--warning)"];
  function series(names: [string, string][]): ChartSeries[] {
    return names.map(([key, name], i) => ({
      name,
      color: colors[i],
      points: (metrics?.time_series[key]?.values ?? []).map(([t, v]) => [t, Number(v)] as [number, number]),
    }));
  }

  const charts = $derived([
    { title: "CPU", format: percent, series: series([["cpu", "CPU"]]) },
    { title: "Disk IOPS", format: rate, series: series([["disk.0.iops.read", "Read"], ["disk.0.iops.write", "Write"]]) },
    { title: "Disk bandwidth", format: bytes, series: series([["disk.0.bandwidth.read", "Read"], ["disk.0.bandwidth.write", "Write"]]) },
    { title: "Network packets", format: rate, series: series([["network.0.pps.in", "In"], ["network.0.pps.out", "Out"]]) },
    { title: "Network bandwidth", format: bytes, series: series([["network.0.bandwidth.in", "In"], ["network.0.bandwidth.out", "Out"]]) },
  ]);
</script>

<section>
  <div class="head">
    <h2>Graphs</h2>
    <div class="ranges" role="group" aria-label="Time range">
      {#each ranges as r (r.id)}
        <Button variant={r.id === rangeId ? "primary" : "secondary"} aria-pressed={r.id === rangeId} onclick={() => (rangeId = r.id)}>{r.label}</Button>
      {/each}
    </div>
  </div>
  <p class="muted">HLab samples every 10 s and keeps the samples 24 hours; a stopped server has no samples.</p>
  {#if !metrics}
    <p class="muted" aria-busy="true">Loading the metrics…</p>
  {:else}
    {#each charts as c (c.title)}
      <LineChart
        title={c.title}
        series={c.series}
        start={Date.parse(metrics.start) / 1000}
        end={Date.parse(metrics.end) / 1000}
        step={metrics.step}
        format={c.format}
      />
    {/each}
  {/if}
</section>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 720px;
  }
  .ranges {
    display: flex;
    gap: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
</style>
