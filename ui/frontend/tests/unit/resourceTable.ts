// Table helper for the unit tests of the resource actions modules
// (lib/resources/*): finds an entry and checks the request it builds and its
// confirmation. Not a test file itself.
import { expect } from "@playwright/test";
import { request, type Confirm, type Input, type ResourceAction } from "../../src/lib/resourceActions";

/** id is the resource ID every case runs against. */
export const id = 42;

/** ResourceModule is what a lib/resources/<resource>.ts module exports. */
export type ResourceModule = {
  create?: ResourceAction;
  menu: ResourceAction[];
  tabs: Record<string, ResourceAction>;
};

export type Ref = "create" | { menu: string } | { tab: string };

export type Case = {
  name: string;
  ref: Ref;
  input?: Input;
  want: { method: string; path: string; body?: unknown };
  confirm: Confirm;
};

/** entry finds the create entry, a menu entry by its label or a tab action by its id. */
export function entry(mod: ResourceModule, ref: Ref): ResourceAction {
  if (ref === "create") {
    if (!mod.create) throw new Error("no create entry");
    return mod.create;
  }
  if ("menu" in ref) {
    const a = mod.menu.find((x) => x.label === ref.menu);
    if (!a) throw new Error(`no menu entry labelled ${ref.menu}`);
    return a;
  }
  const a = mod.tabs[ref.tab];
  if (!a) throw new Error(`no tab action ${ref.tab}`);
  return a;
}

/** check compares the request and the confirmation of one case. */
export function check(mod: ResourceModule, c: Case) {
  const a = entry(mod, c.ref);
  const got = request(a, id, c.input ?? {});
  expect(got).toBeDefined();
  expect(got!.method).toBe(c.want.method);
  expect(got!.path).toBe(c.want.path);
  if (c.want.body === undefined) {
    // An action without parameters may send no body or an empty object.
    expect([undefined, null, {}]).toContainEqual(got!.body);
  } else {
    expect(got!.body).toEqual(c.want.body);
  }
  expect(a.confirm).toBe(c.confirm);
}
