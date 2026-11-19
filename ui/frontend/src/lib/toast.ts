// Action toasts: a running action shows with a spinner and then its result;
// successes close by themselves, errors stay until the user closes them.
import { writable } from "svelte/store";

export type Toast = {
  id: number;
  kind: "progress" | "success" | "error";
  title: string;
  message?: string;
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

// action runs one user action under a toast. It resolves to the result, or
// to undefined after showing the error. done words the success message.
export async function action<T>(
  title: string,
  run: () => Promise<T>,
  done?: (result: T) => string,
): Promise<T | undefined> {
  const id = next++;
  put({ id, kind: "progress", title });
  try {
    const result = await run();
    put({ id, kind: "success", title, message: done ? done(result) : "Done" });
    setTimeout(() => dismiss(id), successFor);
    return result;
  } catch (err) {
    put({ id, kind: "error", title, message: errorText(err) });
    return undefined;
  }
}
