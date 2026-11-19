<script lang="ts">
  import { CircleAlert, CircleCheck, LoaderCircle, X } from "@lucide/svelte";
  import Button from "./Button.svelte";
  import { dismiss, toasts } from "./toast";
</script>

<!-- Mounted once in the shell; shows every toast of lib/toast.ts. -->
<div class="toasts" aria-live="polite">
  {#each $toasts as t (t.id)}
    <div class={["toast", t.kind]} role={t.kind === "error" ? "alert" : undefined}>
      <span class="icon" aria-hidden="true">
        {#if t.kind === "progress"}<LoaderCircle size={16} class="spin" />
        {:else if t.kind === "success"}<CircleCheck size={16} />
        {:else}<CircleAlert size={16} />{/if}
      </span>
      <div class="text">
        <strong>{t.title}</strong>
        {#if t.message}<div>{t.message}</div>{/if}
      </div>
      {#if t.kind !== "progress"}
        <Button variant="icon" label="Dismiss" onclick={() => dismiss(t.id)}><X size={16} aria-hidden="true" /></Button>
      {/if}
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    right: var(--s4);
    bottom: var(--s4);
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    width: 360px;
    z-index: 100;
  }
  .toast {
    display: flex;
    align-items: flex-start;
    gap: var(--s2);
    padding: var(--s3);
    border: 1px solid var(--border);
    border-left: 4px solid var(--accent);
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: 0 4px 12px rgb(0 0 0 / 0.15);
  }
  .success {
    border-left-color: var(--success);
  }
  .error {
    border-left-color: var(--error);
  }
  .icon {
    display: flex;
    padding-top: 2px;
  }
  .success .icon {
    color: var(--success);
  }
  .error .icon {
    color: var(--error);
  }
  .text {
    flex: 1;
    overflow-wrap: anywhere;
  }
</style>
