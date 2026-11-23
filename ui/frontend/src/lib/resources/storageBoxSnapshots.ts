// Storage box snapshot actions of the Snapshots tab, the snapshot plan body
// and the snapshots a rollback deletes. The resource ID is the box's;
// input.snapshot is the snapshot's ID.
import { pick, postAction, type Input, type ResourceAction } from "../resourceActions";

const base = "storage_boxes";

const at = (box: number, i: Input) => `/storage_boxes/${box}/snapshots/${i.snapshot}`;

/** tabs are the actions of the Snapshots tab and its row menu. */
export const tabs: Record<string, ResourceAction> = {
  create: {
    id: "create",
    label: "Create snapshot",
    method: "POST",
    path: (box) => `/storage_boxes/${box}/snapshots`,
    body: (i) => ({ description: i.description }),
    confirm: "none",
    needsInput: true,
  },
  update: {
    id: "update",
    label: "Edit description",
    method: "PUT",
    path: at,
    body: (i) => ({ description: i.description }),
    confirm: "none",
    needsInput: true,
  },
  delete: { id: "delete", label: "Delete", method: "DELETE", path: at, confirm: "plain", needsInput: false },
  rollback: postAction(base, "rollback", "rollback_snapshot", {
    label: "Roll back",
    body: (i) => ({ snapshot: i.snapshot }),
    confirm: "plain",
  }),
  enable_plan: postAction(base, "enable_plan", "enable_snapshot_plan", {
    label: "Set plan",
    body: (i) => pick(i, ["max_snapshots", "minute", "hour", "day_of_week", "day_of_month"]),
    needsInput: true,
  }),
  disable_plan: postAction(base, "disable_plan", "disable_snapshot_plan", { label: "Disable plan", confirm: "plain" }),
};

export type PlanInput = {
  max_snapshots: number;
  hour: number;
  minute: number;
  day_of_week: "every" | 1 | 2 | 3 | 4 | 5 | 6 | 7;
  day_of_month: "every" | number;
};

/** planBody turns the dialog's values into the API body: "every" -> null, Monday 1 .. Sunday 7. */
export function planBody(p: PlanInput): {
  max_snapshots: number;
  minute: number;
  hour: number;
  day_of_week: number | null;
  day_of_month: number | null;
} {
  return {
    max_snapshots: p.max_snapshots,
    minute: p.minute,
    hour: p.hour,
    day_of_week: p.day_of_week === "every" ? null : p.day_of_week,
    day_of_month: p.day_of_month === "every" ? null : p.day_of_month,
  };
}

export type Snap = { id: number; name: string; created: string };

/** newerThan lists the snapshots created after target (the ones a rollback deletes), oldest first. */
export function newerThan(snapshots: Snap[], target: Snap): Snap[] {
  const t = Date.parse(target.created);
  return snapshots.filter((s) => Date.parse(s.created) > t).sort((a, b) => Date.parse(a.created) - Date.parse(b.created));
}
