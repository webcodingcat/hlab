<script lang="ts">
  import Button from "../../../lib/Button.svelte";
  import ConfirmDialog from "../../../lib/ConfirmDialog.svelte";
  import Dialog from "../../../lib/Dialog.svelte";
  import SecretDialog from "../../../lib/SecretDialog.svelte";
  import { listAll } from "../../../lib/api";
  import { showError } from "../../../lib/toast";
  import type { Named, Server } from "../servers";
  import { runTab, runTabAnswer } from "./run";

  // The Rescue tab: enable the rescue system with SSH keys after a
  // confirmation, its root password shown once; disable it.
  type Props = { project: string; server: Server; onchanged: () => void };

  let { project, server, onchanged }: Props = $props();

  let keys = $state<Named[]>([]);
  let chosen = $state<number[]>([]);
  let keysOpen = $state(false);
  let confirmOpen = $state(false);
  let password = $state<string | undefined>();
  let secretOpen = $state(false);

  async function open() {
    chosen = [];
    keysOpen = true;
    try {
      keys = await listAll<Named>(project, "/ssh_keys", "ssh_keys");
    } catch (err) {
      showError("Load SSH keys", err);
    }
  }

  function next(e: SubmitEvent) {
    e.preventDefault();
    keysOpen = false;
    confirmOpen = true;
  }

  async function enable() {
    const res = await runTabAnswer(project, server, "enable_rescue", { ssh_keys: chosen });
    if (res === undefined) return;
    if (res.root_password) {
      password = res.root_password;
      secretOpen = true;
    }
    onchanged();
  }

  async function disable() {
    if (await runTab(project, server, "disable_rescue")) onchanged();
  }

  function closeSecret(open: boolean) {
    secretOpen = open;
    if (!open) password = undefined;
  }
</script>

<section>
  <header>
    <h2>Rescue</h2>
    {#if server.rescue_enabled}
      <Button onclick={disable}>Disable rescue</Button>
    {:else}
      <Button variant="primary" onclick={open}>Enable rescue</Button>
    {/if}
  </header>
  <p class="muted">
    {#if server.rescue_enabled}
      Rescue is enabled: the next power on, reboot or reset boots the Linux rescue system. The server's disk stays
      <span class="mono">/dev/sda</span>.
    {:else}
      Rescue is disabled. The rescue system is a Linux system with its own disk for repairing the server's disk.
    {/if}
  </p>
</section>

<Dialog bind:open={keysOpen} title={`Enable rescue for ${server.name}`}>
  <form onsubmit={next}>
    <fieldset>
      <legend>SSH keys</legend>
      {#each keys as k (k.id)}
        <label class="check"><input type="checkbox" value={k.id} bind:group={chosen} />{k.name}</label>
      {:else}<p class="muted">No SSH keys: log in with the root password, which shows once.</p>{/each}
    </fieldset>
    <div class="actions">
      <Button onclick={() => (keysOpen = false)}>Cancel</Button>
      <Button type="submit" variant="primary">Enable rescue</Button>
    </div>
  </form>
</Dialog>

<ConfirmDialog bind:open={confirmOpen} title="Enable rescue" confirmLabel="Enable rescue" onconfirm={enable}>
  <p>
    <strong>{server.name}</strong> boots the rescue system at its next power on, reboot or reset. Everything written to
    the rescue disk is lost when rescue ends.
  </p>
</ConfirmDialog>

<SecretDialog bind:open={() => secretOpen, closeSecret} title="Root password" label="password" value={password} />

<style>
  section {
    max-width: 960px;
    margin-bottom: var(--s6);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--s3);
  }
  fieldset {
    border: none;
    margin: 0;
    padding: 0;
  }
  legend {
    font-weight: 600;
    margin-bottom: var(--s2);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }
  .mono {
    font-family: var(--mono);
  }
  .muted {
    color: var(--muted);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
