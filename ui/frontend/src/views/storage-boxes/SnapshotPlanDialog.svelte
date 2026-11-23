<script module lang="ts">
  /** weekdays are the API's day_of_week values, Monday 1 .. Sunday 7. */
  export const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import { planBody, type PlanInput } from "../../lib/resources/storageBoxSnapshots";

  // The dialog of "Set plan": max snapshots, the time in UTC and the days.
  // It opens with the current plan (or defaults) and hands the API body to
  // onsave, built by planBody.
  type Plan = Record<string, number | null> | null;
  type Props = { open: boolean; plan: Plan; onsave: (body: ReturnType<typeof planBody>) => void };

  let { open = $bindable(), plan, onsave }: Props = $props();

  let maxSnapshots = $state(10);
  let hour = $state(0);
  let minute = $state(0);
  let dayOfWeek = $state("every");
  let dayOfMonth = $state("every");

  // Each opening starts from the plan of that moment; a later refresh of
  // the box does not overwrite what is typed.
  $effect(() => {
    if (open) untrack(fill);
  });

  function fill() {
    maxSnapshots = plan?.max_snapshots ?? 10;
    hour = plan?.hour ?? 0;
    minute = plan?.minute ?? 0;
    dayOfWeek = plan?.day_of_week ? String(plan.day_of_week) : "every";
    dayOfMonth = plan?.day_of_month ? String(plan.day_of_month) : "every";
  }

  function save(e: SubmitEvent) {
    e.preventDefault();
    const input: PlanInput = {
      max_snapshots: maxSnapshots,
      hour,
      minute,
      day_of_week: dayOfWeek === "every" ? "every" : (Number(dayOfWeek) as PlanInput["day_of_week"]),
      day_of_month: dayOfMonth === "every" ? "every" : Number(dayOfMonth),
    };
    open = false;
    onsave(planBody(input));
  }
</script>

<Dialog bind:open title="Snapshot plan">
  <form onsubmit={save}>
    <Field label="Max snapshots" hint="The oldest automatic snapshot is deleted when the plan takes one more.">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} type="number" min="1" required bind:value={maxSnapshots} />
      {/snippet}
    </Field>
    <div class="time">
      <Field label="Hour" hint="0 to 23, UTC">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="0" max="23" required bind:value={hour} />
        {/snippet}
      </Field>
      <Field label="Minute" hint="0 to 59, UTC">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="0" max="59" required bind:value={minute} />
        {/snippet}
      </Field>
    </div>
    <Field label="Day of week">
      {#snippet children(c)}
        <select id={c.id} bind:value={dayOfWeek}>
          <option value="every">Every day</option>
          {#each weekdays as d, i (d)}<option value={String(i + 1)}>{d}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <Field label="Day of month">
      {#snippet children(c)}
        <select id={c.id} bind:value={dayOfMonth}>
          <option value="every">Every day</option>
          {#each Array.from({ length: 31 }, (_, i) => i + 1) as d (d)}<option value={String(d)}>{d}</option>{/each}
        </select>
      {/snippet}
    </Field>
    <div class="actions">
      <Button onclick={() => (open = false)}>Cancel</Button>
      <Button type="submit" variant="primary">Save</Button>
    </div>
  </form>
</Dialog>

<style>
  form {
    display: flex;
    flex-direction: column;
  }
  input,
  select {
    width: 100%;
  }
  .time {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--s3);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
</style>
