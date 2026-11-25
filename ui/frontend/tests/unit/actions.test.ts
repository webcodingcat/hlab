// Unit tests of the action polling: waitAction follows one action until it
// ends, runAction runs a request under a progress toast and waits for its
// action and every next action; and which entries confirm a delete with the
// typed name. They need no browser.
import { test, expect } from "@playwright/test";
import { get } from "svelte/store";
import { ActionError, runAction, waitAction, type Action, type Poll } from "../../src/lib/actions";
import { toasts, type Toast } from "../../src/lib/toast";
import type { Confirm } from "../../src/lib/resourceActions";
import * as floatingIps from "../../src/lib/resources/floatingIps";
import * as networks from "../../src/lib/resources/networks";
import * as storageBoxSnapshots from "../../src/lib/resources/storageBoxSnapshots";
import * as volumes from "../../src/lib/resources/volumes";
import { tabActions } from "../../src/lib/serverActions";
import { entry } from "./resourceTable";

function act(id: number, status: Action["status"], progress: number, error: Action["error"] = null): Action {
  return { id, command: "poweroff", status, progress, error };
}

/** fakePoll answers GET /actions/{id} with the next state of that action and records the IDs. */
function fakePoll(states: Record<number, Action[]>): Poll & { polled: number[] } {
  const polled: number[] = [];
  return {
    polled,
    get: async (id: number) => {
      polled.push(id);
      const next = states[id]?.shift();
      if (!next) throw new Error(`no more states for action ${id}`);
      return next;
    },
    sleep: async () => {},
  };
}

/** watch records every state of the toasts with the given title. */
function watch(title: string): { seen: Toast[]; last: () => Toast | undefined; stop: () => void } {
  const seen: Toast[] = [];
  const stop = toasts.subscribe((all) => {
    for (const t of all) if (t.title === title) seen.push({ ...t });
  });
  return {
    seen,
    last: () => get(toasts).find((t) => t.title === title),
    stop,
  };
}

function apiError(code: string, message: string): Error {
  return Object.assign(new Error(message), { code });
}

test.describe("UI-02 polling", () => {
  test("waitAction polls until done and reports every polled progress", async () => {
    const poll = fakePoll({ 7: [act(7, "running", 40), act(7, "running", 80), act(7, "success", 100)] });
    const progress: number[] = [];
    const done = await waitAction(act(7, "running", 0), poll, (a) => progress.push(a.progress));
    expect(done.status).toBe("success");
    expect(done.id).toBe(7);
    expect(poll.polled).toEqual([7, 7, 7]);
    expect(progress).toEqual([40, 80, 100]);
  });

  test("waitAction throws ActionError with the action's code and message", async () => {
    const poll = fakePoll({
      8: [act(8, "running", 50), act(8, "error", 100, { code: "action_failed", message: "the disk is gone" })],
    });
    const err = await waitAction(act(8, "running", 0), poll).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ActionError);
    expect((err as ActionError).code).toBe("action_failed");
    expect((err as ActionError).message).toBe("the disk is gone");
  });

  test("runAction waits for action and next_actions under a progress toast that ends as success", async () => {
    const title = "Power off server web-1";
    const w = watch(title);
    const poll = fakePoll({
      1: [act(1, "running", 50), act(1, "success", 100)],
      2: [act(2, "success", 100)],
    });
    const result = { action: act(1, "running", 0), next_actions: [act(2, "running", 0)] };
    const got = await runAction(title, async () => result, poll);
    w.stop();
    expect(got).toEqual(result);
    expect(poll.polled).toEqual([1, 1, 2]);
    expect(w.seen.some((t) => t.kind === "progress" && t.progress === 50)).toBe(true);
    expect(w.last()?.kind).toBe("success");
  });

  test("runAction ends an action error as an error toast with code and message", async () => {
    const title = "Rebuild server web-2";
    const w = watch(title);
    const poll = fakePoll({ 3: [act(3, "error", 100, { code: "server_error", message: "rebuild failed" })] });
    const got = await runAction(title, async () => ({ action: act(3, "running", 0) }), poll);
    w.stop();
    expect(got).toBeUndefined();
    const last = w.last();
    expect(last?.kind).toBe("error");
    expect(last?.message).toBe("server_error: rebuild failed");
  });

  test("runAction ends a failing request as an error toast without polling", async () => {
    const title = "Reset server web-3";
    const w = watch(title);
    const poll = fakePoll({});
    const got = await runAction(
      title,
      async () => {
        throw apiError("conflict", "server is locked");
      },
      poll,
    );
    w.stop();
    expect(got).toBeUndefined();
    expect(poll.polled).toEqual([]);
    const last = w.last();
    expect(last?.kind).toBe("error");
    expect(last?.message).toBe("conflict: server is locked");
  });
});

// Every delete of something with a name asks for the typed name; entries
// without a name of their own and destructive actions other than delete keep
// the plain confirm. One case per kind of confirm entry, one resource each.
test.describe("UI-09 typed-name delete", () => {
  const cases: { name: string; confirm: () => Confirm | undefined; want: Confirm }[] = [
    {
      name: "resource delete from the actions menu",
      confirm: () => entry(floatingIps, { menu: "Delete" }).confirm,
      want: "typed-name",
    },
    {
      name: "delete of a named sub-entry in a detail tab",
      confirm: () => entry({ menu: [], tabs: storageBoxSnapshots.tabs }, { tab: "delete" }).confirm,
      want: "typed-name",
    },
    { name: "delete of a server's snapshot or backup", confirm: () => tabActions.delete_image?.confirm, want: "typed-name" },
    {
      name: "delete of an entry without a name",
      confirm: () => entry(networks, { tab: "delete_subnet" }).confirm,
      want: "plain",
    },
    {
      name: "destructive action other than delete",
      confirm: () => entry(volumes, { menu: "Detach" }).confirm,
      want: "plain",
    },
  ];
  for (const c of cases) test(c.name, () => expect(c.confirm()).toBe(c.want));
});
