// Unit tests of the fault display and request logic: the parameter line,
// target and remaining time of the faults table, and the POST body the
// create form sends. They need no browser.
import { test, expect } from "@playwright/test";
import {
  defaultForm,
  formField,
  paramsSummary,
  remaining,
  requestBody,
  showsSeed,
  targetText,
  type Fault,
  type FaultForm,
  type FaultKind,
  type FaultTarget,
} from "../../src/lib/faults";

test.describe("UI-16 fault table texts", () => {
  const summaries: { name: string; kind: FaultKind; params: Fault["params"]; want: string }[] = [
    {
      name: "api_error with code, percent, only and resource",
      kind: "api_error",
      params: { code: "server_error", percent: 20, only: "write", resource: "servers" },
      want: "20 % server_error, writes, servers",
    },
    {
      name: "api_error without only and resource",
      kind: "api_error",
      params: { code: "server_error", percent: 20 },
      want: "20 % server_error",
    },
    { name: "rate_limit", kind: "rate_limit", params: { per_hour: 100 }, want: "100 per hour" },
    {
      name: "network cut",
      kind: "network",
      params: { traffic: "public", loss_percent: 100 },
      want: "public, 100 % loss",
    },
    {
      name: "network loss",
      kind: "network",
      params: { traffic: "private", loss_percent: 30 },
      want: "private, 30 % loss",
    },
    {
      name: "latency with jitter",
      kind: "latency",
      params: { delay_ms: 50, jitter_ms: 10 },
      want: "50 ms ± 10 ms",
    },
    { name: "latency without jitter", kind: "latency", params: { delay_ms: 50 }, want: "50 ms" },
    { name: "disk_slow", kind: "disk_slow", params: { iops: 200 }, want: "200 IOPS" },
    { name: "server_crash", kind: "server_crash", params: null, want: "–" },
  ];
  for (const c of summaries) {
    test(`paramsSummary: ${c.name}`, () => {
      expect(paramsSummary({ kind: c.kind, params: c.params })).toBe(c.want);
    });
  }

  const names = new Map<string, string>([
    ["server:42", "web-1"],
    ["volume:7", "data"],
    ["storage_box:3", "backup"],
  ]);
  const targets: { name: string; target: FaultTarget | null; names: Map<string, string>; want: string }[] = [
    { name: "no target", target: null, names, want: "Project" },
    { name: "locations", target: { type: "locations", from: "fsn1", to: "hel1" }, names, want: "fsn1 ↔ hel1" },
    { name: "known server", target: { type: "server", id: 42 }, names, want: "web-1" },
    { name: "known volume", target: { type: "volume", id: 7 }, names, want: "data" },
    { name: "known storage box", target: { type: "storage_box", id: 3 }, names, want: "backup" },
    { name: "unknown server", target: { type: "server", id: 42 }, names: new Map(), want: "Server 42" },
  ];
  for (const c of targets) {
    test(`targetText: ${c.name}`, () => {
      expect(targetText(c.target, c.names)).toBe(c.want);
    });
  }
});

test.describe("UI-16 remaining time", () => {
  const now = new Date("2026-11-24T10:00:00Z");
  const after = (seconds: number) => new Date(now.getTime() + seconds * 1000).toISOString();
  const cases: { name: string; ends: string | null; want: string }[] = [
    { name: "without end", ends: null, want: "until ended" },
    { name: "4 min 1 s", ends: after(241), want: "5 min left" },
    { name: "exactly 4 min", ends: after(240), want: "4 min left" },
    { name: "45 s", ends: after(45), want: "45 s left" },
    { name: "past", ends: after(-10), want: "0 s left" },
  ];
  for (const c of cases) {
    test(c.name, () => {
      expect(remaining(c.ends, now)).toBe(c.want);
    });
  }
});

test.describe("UI-16 create request", () => {
  type Want = {
    target: FaultTarget | null;
    params: Record<string, string | number>;
    seed?: number;
    duration?: string;
    showsSeed: boolean;
  };
  // Every case sets a seed; it must only reach the body where the kind takes one.
  const cases: { name: string; kind: FaultKind; fields: Partial<FaultForm>; want: Want }[] = [
    {
      name: "server_crash",
      kind: "server_crash",
      fields: { target: "server:42", seed: "42" },
      want: { target: { type: "server", id: 42 }, params: {}, showsSeed: false },
    },
    {
      name: "server_freeze",
      kind: "server_freeze",
      fields: { target: "server:42", seed: "42" },
      want: { target: { type: "server", id: 42 }, params: {}, showsSeed: false },
    },
    {
      name: "network loss with seed",
      kind: "network",
      fields: { target: "server:42", traffic: "private", lossPercent: "30", seed: "42" },
      want: {
        target: { type: "server", id: 42 },
        params: { traffic: "private", loss_percent: 30 },
        seed: 42,
        showsSeed: true,
      },
    },
    {
      name: "network cut without seed",
      kind: "network",
      fields: { target: "server:42", traffic: "public", lossPercent: "100", seed: "42" },
      want: { target: { type: "server", id: 42 }, params: { traffic: "public", loss_percent: 100 }, showsSeed: false },
    },
    {
      name: "latency between locations",
      kind: "latency",
      fields: { targetMode: "locations", from: "fsn1", to: "hel1", delayMs: "50", jitterMs: "10", seed: "42" },
      want: {
        target: { type: "locations", from: "fsn1", to: "hel1" },
        params: { delay_ms: 50, jitter_ms: 10 },
        showsSeed: false,
      },
    },
    {
      name: "api_error with only and resource, seed empty",
      kind: "api_error",
      fields: { code: "server_error", percent: "20", only: "write", resource: "servers", seed: "" },
      want: {
        target: null,
        params: { code: "server_error", percent: 20, only: "write", resource: "servers" },
        showsSeed: true,
      },
    },
    {
      name: "api_error for All with seed",
      kind: "api_error",
      fields: { code: "conflict", percent: "50", seed: "7" },
      want: { target: null, params: { code: "conflict", percent: 50 }, seed: 7, showsSeed: true },
    },
    {
      name: "rate_limit for 5 minutes",
      kind: "rate_limit",
      fields: { perHour: "100", seed: "42", duration: "5m" },
      want: { target: null, params: { per_hour: 100 }, duration: "5m", showsSeed: false },
    },
    {
      name: "volume_unavailable with custom duration",
      kind: "volume_unavailable",
      fields: { target: "volume:7", seed: "42", duration: "custom", customDuration: "90s" },
      want: { target: { type: "volume", id: 7 }, params: {}, duration: "90s", showsSeed: false },
    },
    {
      name: "disk_slow",
      kind: "disk_slow",
      fields: { target: "server:42", iops: "200", seed: "42" },
      want: { target: { type: "server", id: 42 }, params: { iops: 200 }, showsSeed: false },
    },
    {
      name: "storage_box_unavailable",
      kind: "storage_box_unavailable",
      fields: { target: "storage_box:3", seed: "42" },
      want: { target: { type: "storage_box", id: 3 }, params: {}, showsSeed: false },
    },
  ];
  for (const c of cases) {
    test(`requestBody: ${c.name}`, () => {
      const form: FaultForm = { ...defaultForm(c.kind), ...c.fields };
      const body = requestBody(form);
      expect(body.kind).toBe(c.kind);
      expect(body.target).toEqual(c.want.target);
      expect(body.params ?? {}).toEqual(c.want.params);
      expect(body.seed).toBe(c.want.seed);
      expect(body.duration).toBe(c.want.duration);
      expect(showsSeed(form)).toBe(c.want.showsSeed);
    });
  }

  const fields: { api: string; want: string }[] = [
    { api: "params.percent", want: "percent" },
    { api: "target.from", want: "from" },
    { api: "duration", want: "duration" },
  ];
  for (const c of fields) {
    test(`formField: ${c.api}`, () => {
      expect(formField(c.api)).toBe(c.want);
    });
  }
});
