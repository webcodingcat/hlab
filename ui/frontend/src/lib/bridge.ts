// Wrapper around the Wails runtime: bound Go methods by their fully
// qualified name, the management API through Service.Call, and events.
import { Call, Events } from "@wailsio/runtime";

const bridgePkg = "github.com/webcodingcat/hlab/internal/bridge";

/** Response is one answer of the management API as Service.Call returns it. */
export type Response = { status: number; body: unknown };

/** ApiError is a Hetzner or management error object, or a failed call. */
export class ApiError extends Error {
  code: string;
  fields?: Record<string, string[]>;

  constructor(code: string, message: string, fields?: Record<string, string[]>) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    if (fields) this.fields = fields;
  }
}

/** service calls a method of a bound bridge type. */
export function service<T>(type: "Service" | "Cloud" | "Logs" | "Console", method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(`${bridgePkg}.${type}.${method}`, ...args) as Promise<T>;
}

/** call sends one request to /mgmt/v1<path>; a body is sent as JSON. */
export function call(method: string, path: string, body?: unknown): Promise<Response> {
  const text = body === undefined ? "" : JSON.stringify(body);
  return service<Response>("Service", "Call", method, path, text);
}

/** mgmt sends one request and returns its body; an error status throws ApiError. */
export async function mgmt<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await call(method, path, body);
  } catch (err) {
    throw new ApiError("call_failed", err instanceof Error ? err.message : String(err));
  }
  if (res.status >= 400) throw toApiError(res);
  return res.body as T;
}

type ErrorBody = {
  error?: {
    code?: string;
    message?: string;
    details?: { fields?: { name?: string; messages?: string[] }[] } | null;
  };
};

function toApiError(res: Response): ApiError {
  const e = (res.body as ErrorBody | null)?.error;
  const code = e?.code ?? "server_error";
  const message = e?.message ?? `request failed with status ${res.status}`;
  let fields: Record<string, string[]> | undefined;
  for (const f of e?.details?.fields ?? []) {
    if (!f.name) continue;
    fields ??= {};
    fields[f.name] = f.messages ?? [];
  }
  return new ApiError(code, message, fields);
}

/** on listens to an event the tray emits; the result removes the listener. */
export function on<T>(event: "cloud" | "logs", fn: (data: T) => void): () => void {
  return Events.On(event, (ev) => fn(ev.data as T));
}

/** pickISOFile opens the Windows file dialog for one .iso file; "" when cancelled. */
export function pickISOFile(): Promise<string> {
  return Call.ByName("main.Files.PickISOFile") as Promise<string>;
}

/** openConsoleWindow opens the console window of a server, or brings its open one to the front. */
export function openConsoleWindow(project: string, id: number, name: string): Promise<void> {
  return Call.ByName("main.Windows.OpenConsole", project, id, name) as Promise<void>;
}

/** openConsoleProxy starts the tray's loopback proxy to a console wss URL and returns its ws URL. */
export function openConsoleProxy(wssURL: string): Promise<string> {
  return service<string>("Console", "Open", wssURL);
}
