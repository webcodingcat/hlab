// Display and request logic of the fault pages: the texts of the faults
// table, the top bar pill and the POST body of the create form. Pure; the
// pages load and send through the bridge themselves.

export type FaultKind =
  | "server_crash"
  | "server_freeze"
  | "network"
  | "latency"
  | "api_error"
  | "rate_limit"
  | "volume_unavailable"
  | "disk_slow"
  | "storage_box_unavailable";

export type FaultTarget =
  | { type: "server" | "volume" | "storage_box"; id: number }
  | { type: "locations"; from: string; to: string };

/** A fault as the management API returns it. */
export type Fault = {
  id: number;
  project: string;
  kind: FaultKind;
  target: FaultTarget | null;
  params: Record<string, string | number> | null;
  seed: number | null;
  created: string;
  ends: string | null;
};

type TargetType = "server" | "volume" | "storage_box" | "locations";

/** One entry per kind in form order: readable label, one-line explanation,
 *  the target types it takes and whether it can take a seed. */
export const faultKinds: { kind: FaultKind; label: string; explanation: string; targets: TargetType[]; seed: boolean }[] = [
  {
    kind: "server_crash",
    label: "Server crash",
    explanation: "Turns the server off as if its host failed; it cannot be started until the fault ends.",
    targets: ["server"],
    seed: false,
  },
  {
    kind: "server_freeze",
    label: "Server freeze",
    explanation: "Pauses the server; it stops answering until the fault ends or a power action runs.",
    targets: ["server"],
    seed: false,
  },
  {
    kind: "network",
    label: "Network",
    explanation: "Drops a share of the server's packets on its public or private network; 100 % cuts it off.",
    targets: ["server"],
    seed: true,
  },
  {
    kind: "latency",
    label: "Latency",
    explanation: "Delays the packets of a server, or between the servers of two locations.",
    targets: ["server", "locations"],
    seed: false,
  },
  {
    kind: "api_error",
    label: "API error",
    explanation: "Answers a share of the project's API requests with an error.",
    targets: [],
    seed: true,
  },
  {
    kind: "rate_limit",
    label: "Rate limit",
    explanation: "Limits the project's API requests per hour and refuses the rest.",
    targets: [],
    seed: false,
  },
  {
    kind: "volume_unavailable",
    label: "Volume unavailable",
    explanation: "Removes an attached volume's disk from its server until the fault ends.",
    targets: ["volume"],
    seed: false,
  },
  {
    kind: "disk_slow",
    label: "Slow disk",
    explanation: "Limits the IOPS of a volume or of a server's system disk.",
    targets: ["server", "volume"],
    seed: false,
  },
  {
    kind: "storage_box_unavailable",
    label: "Storage box unavailable",
    explanation: "Makes a storage box unreachable until the fault ends.",
    targets: ["storage_box"],
    seed: false,
  },
];

function kindInfo(kind: FaultKind) {
  return faultKinds.find((k) => k.kind === kind);
}

/** kindLabel is the readable name of a kind; an unknown kind shows as given. */
export function kindLabel(kind: FaultKind): string {
  return kindInfo(kind)?.label ?? kind;
}

/** "20 % server_error, writes, servers", "public, 100 % loss", "50 ms ± 10 ms",
 *  "100 per hour", "200 IOPS"; "–" without parameters. */
export function paramsSummary(f: Pick<Fault, "kind" | "params">): string {
  const p = f.params;
  if (!p || Object.keys(p).length === 0) return "–";
  switch (f.kind) {
    case "api_error": {
      const parts = [`${p.percent} % ${p.code}`];
      if (p.only === "read") parts.push("reads");
      if (p.only === "write") parts.push("writes");
      if (p.resource !== undefined) parts.push(String(p.resource));
      return parts.join(", ");
    }
    case "rate_limit":
      return `${p.per_hour} per hour`;
    case "network":
      return `${p.traffic}, ${p.loss_percent} % loss`;
    case "latency":
      return p.jitter_ms ? `${p.delay_ms} ms ± ${p.jitter_ms} ms` : `${p.delay_ms} ms`;
    case "disk_slow":
      return `${p.iops} IOPS`;
    default:
      return "–";
  }
}

const targetTypeText: Record<string, string> = {
  server: "Server",
  volume: "Volume",
  storage_box: "Storage Box",
};

/** "Project" (no target), "fsn1 ↔ hel1", the resource's name from names
 *  ("server:42" -> "web-1"), or "Server 42" / "Volume 7" / "Storage Box 3"
 *  when the name is unknown. */
export function targetText(t: FaultTarget | null, names: Map<string, string>): string {
  if (t === null) return "Project";
  if (t.type === "locations") return `${t.from} ↔ ${t.to}`;
  return names.get(`${t.type}:${t.id}`) ?? `${targetTypeText[t.type]} ${t.id}`;
}

/** "until ended", "4 min left" (whole minutes rounded up), "45 s left"
 *  under one minute; never negative ("0 s left"). */
export function remaining(ends: string | null, now: Date): string {
  if (ends === null) return "until ended";
  const seconds = Math.max(0, Math.ceil((new Date(ends).getTime() - now.getTime()) / 1000));
  if (seconds < 60) return `${seconds} s left`;
  return `${Math.ceil(seconds / 60)} min left`;
}

/** "1 fault active", "N faults active". */
export function faultsPill(n: number): string {
  return n === 1 ? "1 fault active" : `${n} faults active`;
}

/** The create form's values. Empty strings are unset number fields. */
export type FaultForm = {
  kind: FaultKind;
  targetMode: "server" | "locations";
  target: string; // "server:42"
  from: string;
  to: string;
  code: string;
  percent: string;
  only: "read" | "write" | "";
  resource: string;
  perHour: string;
  traffic: "public" | "private" | "all";
  lossPercent: string;
  delayMs: string;
  jitterMs: string;
  iops: string;
  seed: string;
  duration: "" | "1m" | "5m" | "15m" | "60m" | "custom";
  customDuration: string;
};

/** defaultForm is a new form for kind: error code server_error, Only and
 *  resource type All, traffic All, loss 100 %, until ended; number fields empty. */
export function defaultForm(kind: FaultKind): FaultForm {
  return {
    kind,
    targetMode: "server",
    target: "",
    from: "",
    to: "",
    code: "server_error",
    percent: "",
    only: "",
    resource: "",
    perHour: "",
    traffic: "all",
    lossPercent: "100",
    delayMs: "",
    jitterMs: "",
    iops: "",
    seed: "",
    duration: "",
    customDuration: "",
  };
}

/** Whether the seed field shows: api_error, network with loss below 100. */
export function showsSeed(f: FaultForm): boolean {
  if (f.kind === "api_error") return true;
  return f.kind === "network" && f.lossPercent !== "" && Number(f.lossPercent) < 100;
}

// The form field of each parameter, by its name in the API.
const paramFields: Record<FaultKind, Record<string, keyof FaultForm>> = {
  server_crash: {},
  server_freeze: {},
  network: { traffic: "traffic", loss_percent: "lossPercent" },
  latency: { delay_ms: "delayMs", jitter_ms: "jitterMs" },
  api_error: { code: "code", percent: "percent", only: "only", resource: "resource" },
  rate_limit: { per_hour: "perHour" },
  volume_unavailable: {},
  disk_slow: { iops: "iops" },
  storage_box_unavailable: {},
};

// Parameters sent as text; all others are numbers.
const textParams = new Set(["traffic", "code", "only", "resource"]);

function target(f: FaultForm): FaultTarget | null {
  const types = kindInfo(f.kind)?.targets ?? [];
  if (types.length === 0) return null;
  if (types.includes("locations") && f.targetMode === "locations") {
    return { type: "locations", from: f.from, to: f.to };
  }
  const i = f.target.lastIndexOf(":");
  if (i < 0) return null;
  return { type: f.target.slice(0, i) as "server" | "volume" | "storage_box", id: Number(f.target.slice(i + 1)) };
}

/** The POST body: kind, target, only the kind's params as numbers or
 *  strings (absent "only"/"resource" left out), seed only where showsSeed
 *  and set, duration left out for "until ended", the custom text as typed. */
export function requestBody(f: FaultForm): {
  kind: FaultKind;
  target: FaultTarget | null;
  params?: Record<string, string | number>;
  seed?: number;
  duration?: string;
} {
  const body: ReturnType<typeof requestBody> = { kind: f.kind, target: target(f) };
  const params: Record<string, string | number> = {};
  for (const [name, field] of Object.entries(paramFields[f.kind])) {
    const value = f[field];
    if (value === "") continue;
    params[name] = textParams.has(name) ? value : Number(value);
  }
  if (Object.keys(params).length > 0) body.params = params;
  if (showsSeed(f) && f.seed !== "") body.seed = Number(f.seed);
  if (f.duration === "custom") body.duration = f.customDuration;
  else if (f.duration !== "") body.duration = f.duration;
  return body;
}

// The form field of the API field names outside params.
const otherFields: Record<string, string> = {
  "target.from": "from",
  "target.to": "to",
  "target.type": "target",
  "target.id": "target",
};

/** The form field a management API field name belongs to
 *  ("params.percent" -> "percent", "target.from" -> "from", "duration" -> "duration"). */
export function formField(apiName: string): string {
  if (apiName.startsWith("params.")) {
    const name = apiName.slice("params.".length);
    for (const fields of Object.values(paramFields)) {
      if (fields[name]) return fields[name];
    }
    return name;
  }
  return otherFields[apiName] ?? apiName;
}

/** Top-level resource paths of both APIs for the resource type select. */
export const faultResources: string[] = [
  "actions",
  "certificates",
  "datacenters",
  "firewalls",
  "floating_ips",
  "images",
  "isos",
  "load_balancer_types",
  "load_balancers",
  "locations",
  "networks",
  "placement_groups",
  "pricing",
  "primary_ips",
  "server_types",
  "servers",
  "ssh_keys",
  "storage_box_types",
  "storage_boxes",
  "volumes",
  "zones",
];
