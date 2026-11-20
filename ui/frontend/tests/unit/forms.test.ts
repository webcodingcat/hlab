// Unit tests of the form helpers: API field errors mapped to form fields,
// and label rows. They need no browser.
import { test, expect } from "@playwright/test";
import { formErrors, type FormErrors } from "../../src/lib/formErrors";
import { parseLabel, toLabels, toRows, type LabelRow } from "../../src/lib/labels";

/** apiError has the shape of the bridge's ApiError: code, message and details.fields by name. */
function apiError(code: string, message: string, fields?: Record<string, string[]>): Error {
  return Object.assign(new Error(message), { code }, fields ? { fields } : {});
}

test.describe("UI-02 field errors", () => {
  const known = ["name", "labels", "networks", "public_net"];
  const cases: { name: string; err: unknown; want: FormErrors }[] = [
    {
      name: "message under its known field",
      err: apiError("invalid_input", "invalid input in field 'name'", { name: ["name is already used"] }),
      want: { fields: { name: "name is already used" } },
    },
    {
      name: "several messages joined",
      err: apiError("invalid_input", "invalid input", { name: ["is too long", "contains invalid characters"] }),
      want: { fields: { name: "is too long contains invalid characters" } },
    },
    {
      name: "labels.x to labels",
      err: apiError("invalid_input", "invalid input", { "labels.env": ["invalid label value"] }),
      want: { fields: { labels: "invalid label value" } },
    },
    {
      name: "networks[0] to networks",
      err: apiError("invalid_input", "invalid input", { "networks[0]": ["network not found"] }),
      want: { fields: { networks: "network not found" } },
    },
    {
      name: "a.b to a",
      err: apiError("invalid_input", "invalid input", { "public_net.ipv4": ["primary IP not found"] }),
      want: { fields: { public_net: "primary IP not found" } },
    },
    {
      name: "unknown field to the general line",
      err: apiError("invalid_input", "invalid input in field 'colour'", { colour: ["unknown field"] }),
      want: { fields: {}, general: "invalid_input: invalid input in field 'colour'" },
    },
    {
      name: "error without fields to the general line",
      err: apiError("uniqueness_error", "SSH key with the same fingerprint already exists"),
      want: { fields: {}, general: "uniqueness_error: SSH key with the same fingerprint already exists" },
    },
  ];
  for (const c of cases) {
    test(`formErrors: ${c.name}`, () => {
      const got = formErrors(c.err, known);
      expect(got.fields).toEqual(c.want.fields);
      expect(got.general).toBe(c.want.general);
    });
  }
});

test.describe("UI-02 labels", () => {
  test("toRows lists labels sorted by key", () => {
    expect(toRows({ zone: "b", app: "web", env: "" })).toEqual([
      { key: "app", value: "web" },
      { key: "env", value: "" },
      { key: "zone", value: "b" },
    ]);
  });

  const cases: { name: string; rows: LabelRow[]; want: { labels: Record<string, string> } | { row: number } }[] = [
    {
      name: "skips empty rows",
      rows: [
        { key: "app", value: "web" },
        { key: "", value: "" },
        { key: "env", value: "prod" },
      ],
      want: { labels: { app: "web", env: "prod" } },
    },
    {
      name: "keeps an empty value",
      rows: [{ key: "backup", value: "" }],
      want: { labels: { backup: "" } },
    },
    {
      name: "refuses a duplicate key naming its second row",
      rows: [
        { key: "app", value: "web" },
        { key: "env", value: "prod" },
        { key: "app", value: "db" },
      ],
      want: { row: 2 },
    },
  ];
  for (const c of cases) {
    test(`toLabels: ${c.name}`, () => {
      const got = toLabels(c.rows);
      if ("labels" in c.want) {
        expect(got).toEqual(c.want);
        return;
      }
      expect("error" in got).toBe(true);
      if (!("error" in got)) return;
      expect(got.row).toBe(c.want.row);
      expect(got.error).not.toBe("");
    });
  }
});

test.describe("UI-02 label chips", () => {
  const long = "a".repeat(64);
  const cases: { name: string; text: string; want: LabelRow | "error" }[] = [
    { name: "key=value", text: "env=prod", want: { key: "env", value: "prod" } },
    { name: "key only", text: "backup", want: { key: "backup", value: "" } },
    { name: "key with empty value", text: "backup=", want: { key: "backup", value: "" } },
    { name: "trimmed", text: "  env=prod  ", want: { key: "env", value: "prod" } },
    { name: "63 characters", text: `${"k".repeat(63)}=${"v".repeat(63)}`, want: { key: "k".repeat(63), value: "v".repeat(63) } },
    { name: "dashes, underscores and dots inside", text: "a-b_c.d=x.y-z_1", want: { key: "a-b_c.d", value: "x.y-z_1" } },
    { name: "valid prefix", text: "example.com/app=web", want: { key: "example.com/app", value: "web" } },
    { name: "key over 63 characters", text: `${long}=x`, want: "error" },
    { name: "value over 63 characters", text: `env=${long}`, want: "error" },
    { name: "key starting with a dash", text: "-env=prod", want: "error" },
    { name: "key ending with a dot", text: "env.=prod", want: "error" },
    { name: "value starting with an underscore", text: "env=_prod", want: "error" },
    { name: "value ending with a dash", text: "env=prod-", want: "error" },
    { name: "other character in the key", text: "en$v=prod", want: "error" },
    { name: "other character in the value", text: "env=pr od", want: "error" },
    { name: "invalid prefix", text: "Example_Com/app=web", want: "error" },
    { name: "empty prefix", text: "/app=web", want: "error" },
    { name: "reserved prefix", text: "hetzner.cloud/app=web", want: "error" },
  ];
  for (const c of cases) {
    test(`parseLabel: ${c.name}`, () => {
      const got = parseLabel(c.text);
      if (c.want === "error") {
        expect("error" in got).toBe(true);
        if ("error" in got) expect(got.error).not.toBe("");
        return;
      }
      expect(got).toEqual({ row: c.want });
    });
  }
});
