// Action toasts: a running action shows with a spinner and then its result;
// successes close by themselves, errors stay until the user closes them.
import { writable } from "svelte/store";

export type Toast = {
  id: number;
  kind: "progress" | "success" | "error";
  title: string;
  message?: string;
  /** progress of a running action in percent (0..100), kind "progress" only. */
  progress?: number;
};

export const toasts = writable<Toast[]>([]);

const successFor = 4000;
let next = 1;

function put(t: Toast) {
  toasts.update((all) => (all.some((x) => x.id === t.id) ? all.map((x) => (x.id === t.id ? t : x)) : [...all, t]));
}

export function dismiss(id: number) {
  toasts.update((all) => all.filter((t) => t.id !== id));
}

// errorText shows an API error with its code (Hetzner's or the management
// API's) and message; other errors with their message.
export function errorText(err: unknown): string {
  if (err instanceof Error) {
    const code = (err as { code?: unknown }).code;
    return typeof code === "string" && code ? `${code}: ${err.message}` : err.message;
  }
  return String(err);
}

export function showError(title: string, err: unknown) {
  put({ id: next++, kind: "error", title, message: errorText(err) });
}

// showSuccess shows the result of an action that ran outside a toast; it
// closes by itself.
export function showSuccess(title: string, message: string) {
  const id = next++;
  put({ id, kind: "success", title, message });
  setTimeout(() => dismiss(id), successFor);
}

/** ProgressToast controls one toast of a running action. */
export type ProgressToast = {
  update: (progress: number) => void;
  succeed: (message?: string) => void;
  fail: (err: unknown) => void;
};

// progressToast shows a running action; update sets its progress, succeed
// ends it as success (closes by itself), fail as error (stays).
export function progressToast(title: string, progress?: number): ProgressToast {
  const id = next++;
  put({ id, kind: "progress", title, progress });
  return {
    update: (p) => put({ id, kind: "progress", title, progress: p }),
    succeed: (message = "Done") => {
      put({ id, kind: "success", title, message });
      setTimeout(() => dismiss(id), successFor);
    },
    fail: (err) => put({ id, kind: "error", title, message: errorText(err) }),
  };
}

// action runs one user action under a toast. It resolves to the result, or
// to undefined after showing the error. done words the success message.
export async function action<T>(
  title: string,
  run: () => Promise<T>,
  done?: (result: T) => string,
): Promise<T | undefined> {
  const t = progressToast(title);
  try {
    const result = await run();
    t.succeed(done ? done(result) : "Done");
    return result;
  } catch (err) {
    t.fail(err);
    return undefined;
  }
}
