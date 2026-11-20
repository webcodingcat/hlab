<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import Field from "../../../lib/Field.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Server } from "../servers";
  import { runTab } from "./run";

  // The ISO tab: the ISO in the server's DVD drive, attach one from /isos
  // and detach it.
  type Iso = { id: number; name: string; description: string; deprecation: unknown };
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let isos = $state<Iso[] | undefined>();
  let chosen = $state("");

  $effect(() => {
    listAll<Iso>(project, "/isos", "isos")
      .then((all) => (isos = all.filter((i) => !i.deprecation)))
      .catch((err) => showError("Load ISOs", err));
  });

  async function run(id: string, input: Record<string, unknown> = {}) {
    if (await runTab(project, server, id, input)) onchanged();
  }

  function attach(e: SubmitEvent) {
    e.preventDefault();
    run("attach_iso", { iso: Number(chosen) });
    chosen = "";
  }
</script>

<section>
  <h2>ISO</h2>
  {#if server.iso}
    <div class="attached">
      <span><strong>{server.iso.description || server.iso.name}</strong> is in the DVD drive; the server boots from it after a reset.</span>
      <Button onclick={() => run("detach_iso")}>Detach ISO</Button>
    </div>
  {:else}
    <p class="muted">No ISO is attached.</p>
  {/if}
  <form onsubmit={attach}>
    <Field label="ISO" hint="The first attach of an ISO downloads it, which takes minutes.">
      {#snippet children(c)}
        <select id={c.id} aria-describedby={c.describedby} bind:value={chosen}>
          <option value="" disabled>{isos ? "Choose an ISO" : "Loading the ISOs…"}</option>
          {#each isos ?? [] as i (i.id)}<option value={`${i.id}`}>{i.description || i.name}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <div class="actions">
      <Button type="submit" variant="primary" disabled={chosen === ""}>Attach ISO</Button>
    </div>
  </form>
</section>

<style>
  section {
    max-width: 640px;
    margin-bottom: var(--s6);
  }
  .attached {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    margin-bottom: var(--s4);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  select {
    width: 100%;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    margin-top: var(--s2);
  }
  .muted {
    color: var(--muted);
  }
</style>
