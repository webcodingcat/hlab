// Resource actions: one entry of a resource's actions menu or detail tab,
// with the request it sends and its confirmation, plus builders for the
// entries most resources share. Pure, so the unit tests load it without a
// browser; the Svelte views only render it. A base is the API path segment
// of the resource without slashes, for example "volumes" or "load_balancers".

export type Input = Record<string, unknown>;
export type Confirm = "none" | "plain" | "typed-name";
export type ResourceAction = {
  id: string; // "resize", "assign", "add_subnet", "create", ...
  label: string; // menu or button label
  method: "GET" | "POST" | "PUT" | "DELETE"; // GET only for the zone file export
  path: (id: number, input: Input) => string; // id is the resource's ID (ignored by create)
  body?: (input: Input, id: number) => unknown;
  confirm: Confirm;
  needsInput: boolean; // opens a dialog before it runs
  disabledHint?: string; // set: shown disabled, never sent
};

/** request builds method, path and body; undefined for a disabled entry. */
export function request(
  a: ResourceAction,
  id: number,
  input: Input = {},
): { method: string; path: string; body?: unknown } | undefined {
  if (a.disabledHint !== undefined) return undefined;
  const r: { method: string; path: string; body?: unknown } = { method: a.method, path: a.path(id, input) };
  const body = a.body?.(input, id);
  if (body !== undefined) r.body = body;
  return r;
}

/** postAction is a POST to /<base>/{id}/actions/<act>; confirm "none" and needsInput false unless given. */
export function postAction(
  base: string,
  id: string,
  act: string,
  o: { label: string; body?: ResourceAction["body"]; confirm?: Confirm; needsInput?: boolean },
): ResourceAction {
  return {
    id,
    label: o.label,
    method: "POST",
    path: (rid) => `/${base}/${rid}/actions/${act}`,
    body: o.body,
    confirm: o.confirm ?? "none",
    needsInput: o.needsInput ?? false,
  };
}

/** update is a PUT to /<base>/{id}. */
export function update(
  base: string,
  id: string,
  label: string,
  body: ResourceAction["body"],
  o: { confirm?: Confirm; needsInput?: boolean } = {},
): ResourceAction {
  return {
    id,
    label,
    method: "PUT",
    path: (rid) => `/${base}/${rid}`,
    body,
    confirm: o.confirm ?? "none",
    needsInput: o.needsInput ?? false,
  };
}

/** remove is a DELETE of /<base>/{id} with the id "delete". */
export function remove(base: string, label: string, confirm: Confirm): ResourceAction {
  return { id: "delete", label, method: "DELETE", path: (rid) => `/${base}/${rid}`, confirm, needsInput: false };
}

/** createAt is a POST to /<base> with the id "create". */
export function createAt(base: string, label: string, body: ResourceAction["body"]): ResourceAction {
  return { id: "create", label, method: "POST", path: () => `/${base}`, body, confirm: "none", needsInput: false };
}

/** changeProtection sends the protection flags of input that are booleans (delete, rebuild). */
export function changeProtection(base: string): ResourceAction {
  return postAction(base, "change_protection", "change_protection", {
    label: "Change protection",
    body: (i) => {
      const out: Input = {};
      for (const k of ["delete", "rebuild"]) if (typeof i[k] === "boolean") out[k] = i[k];
      return out;
    },
    needsInput: true,
  });
}

/** changeDnsPtr sets the reverse DNS of input.ip; an empty dns_ptr resets it to the default (null). */
export function changeDnsPtr(base: string): ResourceAction {
  return postAction(base, "change_dns_ptr", "change_dns_ptr", {
    label: "Reverse DNS",
    body: (i) => {
      const ptr = typeof i.dns_ptr === "string" ? i.dns_ptr.trim() : "";
      return { ip: i.ip, dns_ptr: ptr === "" ? null : ptr };
    },
    needsInput: true,
  });
}

/** pick copies the named fields of input that are set. */
export function pick(input: Input, keys: string[]): Input {
  const out: Input = {};
  for (const k of keys) {
    if (input[k] !== undefined) out[k] = input[k];
  }
  return out;
}
