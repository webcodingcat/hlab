<script lang="ts">
  import Table, { type Column } from "./Table.svelte";
  import { kindLabel, paramsSummary, remaining, targetText, type Fault } from "./faults";
  import { href, type ProjectView } from "../state";

  // The table of active faults shared by a project's Faults and Active
  // faults. names maps "server:42" to the resource's name; withProject adds
  // a first Project column with the display names, each a link to that
  // project's Faults. The remaining time is computed whenever faults change
  // (the pages refresh every 5 s). Callers show their own empty state.
  type Props = {
    faults: Fault[];
    names: Map<string, string>;
    withProject?: { name: string; display: string }[];
    onEnd: (f: Fault) => void;
  };

  let { faults, names, withProject, onEnd }: Props = $props();

  const displayOf = (project: string) => withProject?.find((p) => p.name === project)?.display ?? project;

  const now = $derived.by(() => {
    void faults;
    return new Date();
  });

  const columns: Column<Fault>[] = $derived([
    ...(withProject
      ? [{ key: "project", label: "Project", value: (f: Fault) => displayOf(f.project), sortable: true }]
      : []),
    { key: "kind", label: "Kind", value: (f) => kindLabel(f.kind), sortable: true },
    { key: "target", label: "Target", value: (f) => targetText(f.target, names), sortable: true },
    { key: "params", label: "Parameters", value: (f) => paramsSummary(f) },
    { key: "seed", label: "Seed", value: (f) => (f.seed === null ? "" : String(f.seed)) },
    { key: "remaining", label: "Remaining", value: (f) => remaining(f.ends, now) },
  ]);

  const views: Record<string, ProjectView> = { server: "servers", volume: "volumes", storage_box: "storage-boxes" };

  // The detail page of a fault's server, volume or storage box; undefined
  // for a project-wide fault or one between locations.
  function targetHref(f: Fault): string | undefined {
    const t = f.target;
    if (t === null || t.type === "locations") return undefined;
    return href({
      area: "project",
      project: f.project,
      view: views[t.type],
      page: { kind: "detail", id: t.id, tab: "overview" },
    });
  }

  const faultsHref = (project: string) =>
    href({ area: "project", project, view: "faults", page: { kind: "list" } });
</script>

<Table
  title="Faults"
  rows={faults}
  {columns}
  key={(f) => f.id}
  name={(f) => `${displayOf(f.project)} ${kindLabel(f.kind)} ${targetText(f.target, names)}`}
  emptyText="No faults are active."
  actions={(f) => [{ label: "End", onclick: () => onEnd(f) }]}
>
  {#snippet cell(f, c)}
    {#if c.key === "project"}
      <a href={faultsHref(f.project)}>{c.value(f)}</a>
    {:else if c.key === "target" && targetHref(f)}
      <a href={targetHref(f)}>{c.value(f)}</a>
    {:else}{c.value(f)}{/if}
  {/snippet}
</Table>
