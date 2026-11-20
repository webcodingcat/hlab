// Hetzner actions: waitAction follows one action until it ends, runAction
// runs a request under a progress toast and waits for its action and every
// next action. Kept free of the bridge so the unit tests load it without a
// browser.
import { progressToast } from "./toast";

/** Action is a Hetzner action object as the API returns it. */
export type Action = {
  id: number;
  command: string;
  status: "running" | "success" | "error";
  progress: number;
  error: { code: string; message: string } | null;
};

/** ActionResult is the part of an answer that carries actions. */
export type ActionResult = { action?: Action; next_actions?: Action[] };

/** ActionError is an action that ended in "error", with its code and message. */
export class ActionError extends Error {
  code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "ActionError";
    this.code = code;
  }
}

/** Poll reads one action by its ID; sleep waits between two reads (1 s by default). */
export type Poll = { get: (id: number) => Promise<Action>; sleep?: (ms: number) => Promise<void> };

const interval = 1000;

function defaultSleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/** waitAction polls until the action is no longer running; onProgress gets every polled state. */
export async function waitAction(a: Action, poll: Poll, onProgress?: (a: Action) => void): Promise<Action> {
  const sleep = poll.sleep ?? defaultSleep;
  let cur = a;
  while (cur.status === "running") {
    await sleep(interval);
    cur = await poll.get(cur.id);
    onProgress?.(cur);
  }
  if (cur.status === "error") {
    throw new ActionError(cur.error?.code ?? "action_failed", cur.error?.message ?? "the action failed");
  }
  return cur;
}

/**
 * runAction shows a progress toast, runs run(), waits for its action and
 * then every next action, and ends the toast as success or error. It
 * resolves to the result, or to undefined after an error.
 */
export async function runAction<T extends ActionResult>(
  title: string,
  run: () => Promise<T>,
  poll: Poll,
): Promise<T | undefined> {
  const toast = progressToast(title, 0);
  try {
    const result = await run();
    const all = [...(result.action ? [result.action] : []), ...(result.next_actions ?? [])];
    for (const a of all) {
      toast.update(a.progress);
      await waitAction(a, poll, (s) => toast.update(s.progress));
    }
    toast.succeed();
    return result;
  } catch (err) {
    toast.fail(err);
    return undefined;
  }
}
