// Form errors from an API error: Hetzner's details.fields messages under the
// form's fields, everything else on the form's general line. Errors are
// recognised by shape (code, message, fields), so this module needs no bridge.

export type FormErrors = { fields: Record<string, string>; general?: string };

type Shape = { code?: unknown; message?: unknown; fields?: Record<string, string[]> };

/** topLevel maps "labels.x", "networks[0]" and "a.b" to their first name. */
function topLevel(name: string): string {
  return name.split(/[.[]/, 1)[0];
}

/**
 * formErrors maps an error with fields to the form's known fields; messages
 * of one field are joined with a space; unknown names and errors without
 * fields go to general as "<code>: <message>".
 */
export function formErrors(err: unknown, known: string[]): FormErrors {
  const e = (typeof err === "object" && err !== null ? err : {}) as Shape;
  const message = typeof e.message === "string" ? e.message : String(err);
  const general = typeof e.code === "string" && e.code ? `${e.code}: ${message}` : message;
  const out: FormErrors = { fields: {} };
  let unknown = !e.fields || Object.keys(e.fields).length === 0;
  for (const [name, messages] of Object.entries(e.fields ?? {})) {
    const field = topLevel(name);
    if (!known.includes(field)) {
      unknown = true;
      continue;
    }
    const text = messages.join(" ");
    out.fields[field] = out.fields[field] ? `${out.fields[field]} ${text}` : text;
  }
  if (unknown) out.general = general;
  return out;
}
