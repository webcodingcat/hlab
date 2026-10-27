// The web UI: polls the lab's overview and shows one section at a time.
//
// The page builds its elements with the DOM API (never innerHTML), so names and
// labels from the API are shown as text. The section comes from the URL's hash
// (#servers), so a reload or a bookmark keeps it.
"use strict";

const POLL_MS = 2000;

// --- small helpers ------------------------------------------------------------------

// el(tag, props, ...children): an element with properties and child nodes or texts.
function el(tag, props, ...children) {
  const node = document.createElement(tag);
  Object.assign(node, props || {});
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === "") continue;
    node.append(child instanceof Node ? child : String(child));
  }
  return node;
}

function chip(text) {
  return el("span", { className: "chip" }, text);
}

function mono(text) {
  return el("span", { className: "mono" }, text);
}

// state(text, tone): a status with a coloured dot; tone is good, warn, bad or idle.
function state(text, tone) {
  return el("span", { className: `state ${tone}` }, text);
}

function labels(item) {
  return Object.entries(item.labels || {}).map(([k, v]) => chip(v ? `${k}=${v}` : k));
}

function time(iso) {
  return iso ? new Date(iso).toLocaleString() : "";
}

// table(columns, rows): columns are [heading, row => cell] pairs.
function table(columns, rows) {
  if (!rows.length) return el("div", { className: "table-wrap" }, el("p", { className: "empty" }, "none"));
  const head = el("tr", {}, columns.map(([heading]) => el("th", {}, heading)));
  const body = rows.map((row) => el("tr", {}, columns.map(([, cell]) => el("td", {}, cell(row)))));
  return el("div", { className: "table-wrap" }, el("table", {}, el("thead", {}, head), el("tbody", {}, body)));
}

// Names of resources by ID, to show "web (1000001)" instead of a bare ID.
let names = {};

function serverName(id) {
  if (id === null || id === undefined) return el("span", { className: "muted" }, "–");
  return names.servers?.[id] ?? `#${id}`;
}

function networkName(id) {
  return names.networks?.[id] ?? `#${id}`;
}

// --- tones ---------------------------------------------------------------------------

const SERVER_TONES = { running: "good", off: "idle", initializing: "warn", starting: "warn", stopping: "warn", rebuilding: "warn", migrating: "warn", deleting: "bad" };
const ACTION_TONES = { running: "warn", success: "good", error: "bad" };

function tone(map, value) {
  return map[value] || "idle";
}

// --- sections -------------------------------------------------------------------------

function serverState(server) {
  const parts = [state(server.status, tone(SERVER_TONES, server.status))];
  if (server.vm !== (server.status === "off" ? "off" : "on")) parts.push(" ", chip(`VM ${server.vm}`));
  if (server.dead) parts.push(" ", chip("dead"));
  if (server.locked) parts.push(" ", chip("locked"));
  return parts;
}

function serversTable(data) {
  return table(
    [
      ["Name", (s) => [el("strong", {}, s.name), " ", mono(s.id)]],
      ["Status", serverState],
      ["Type", (s) => s.server_type.name],
      ["Location", (s) => s.location.name],
      ["IPv4", (s) => mono(s.public_net.ipv4?.ip ?? "–")],
      ["IPv6", (s) => mono(s.public_net.ipv6?.ip ?? "–")],
      ["Private", (s) => s.private_net.map((n) => chip(`${networkName(n.network)} ${n.ip}`))],
      ["Image", (s) => s.image?.name ?? s.image?.description ?? ""],
      ["Labels", labels],
    ],
    data.servers,
  );
}

function actionsTable(actions) {
  return table(
    [
      ["ID", (a) => mono(a.id)],
      ["Command", (a) => a.command],
      ["Status", (a) => [state(a.status, tone(ACTION_TONES, a.status)), a.error ? el("div", { className: "muted" }, a.error.message) : ""]],
      ["Resources", (a) => a.resources.map((r) => chip(`${r.type} ${r.id}`))],
      ["Started", (a) => time(a.started)],
      ["Finished", (a) => time(a.finished)],
    ],
    actions,
  );
}

function faultText(fault) {
  return Object.entries(fault)
    // the references repeat the names in the lab's own form
    .filter(([key]) => key !== "type" && !key.endsWith("_ref"))
    .map(([key, value]) => `${key}=${value}`)
    .join(" ");
}

function faultsTable(data) {
  const rows = [
    ...data.faults.map((f) => ({ kind: f.type, what: faultText(f) })),
    ...Object.entries(data.netem).map(([link, args]) => ({ kind: "netem", what: `${link}: ${args}` })),
  ];
  const api = data.api_fault;
  if (api.status && api.count) rows.push({ kind: "api", what: `the next ${api.count} requests answer ${api.status}` });
  if (api.delay_ms) rows.push({ kind: "api", what: `every request waits ${api.delay_ms} ms` });
  return table([["Fault", (f) => chip(f.kind)], ["What", (f) => mono(f.what)]], rows);
}

const SECTIONS = [
  {
    id: "overview",
    title: "Overview",
    count: () => "",
    render: (data) => [
      el(
        "div",
        { className: "cards" },
        SECTIONS.filter((s) => s.id !== "overview").map((s) =>
          el("a", { className: "card", href: `#${s.id}` }, el("strong", {}, s.count(data)), el("span", {}, s.title)),
        ),
      ),
      el("h3", {}, "Servers"),
      serversTable(data),
      el("h3", {}, "Running actions"),
      actionsTable(data.actions.filter((a) => a.status === "running")),
      el("h3", {}, "Faults"),
      faultsTable(data),
    ],
  },
  { id: "servers", title: "Servers", count: (d) => d.servers.length, render: (d) => serversTable(d) },
  {
    id: "networks",
    title: "Networks",
    count: (d) => d.networks.length,
    render: (d) =>
      table(
        [
          ["Name", (n) => [el("strong", {}, n.name), " ", mono(n.id)]],
          ["Range", (n) => mono(n.ip_range)],
          ["Subnets", (n) => n.subnets.map((s) => chip(`${s.ip_range} ${s.network_zone}`))],
          ["Routes", (n) => n.routes.map((r) => chip(`${r.destination} → ${r.gateway}`))],
          ["Servers", (n) => n.servers.map((id) => chip(serverName(id)))],
          ["Load balancers", (n) => n.load_balancers.map((id) => chip(names.load_balancers?.[id] ?? `#${id}`))],
          ["Labels", labels],
        ],
        d.networks,
      ),
  },
  {
    id: "ips",
    title: "IPs",
    count: (d) => d.floating_ips.length + d.primary_ips.length,
    render: (d) => [
      el("h3", {}, "Primary IPs"),
      table(
        [
          ["Name", (p) => [el("strong", {}, p.name), " ", mono(p.id)]],
          ["IP", (p) => mono(p.ip)],
          ["Type", (p) => p.type],
          ["Server", (p) => serverName(p.assignee_id)],
          ["Location", (p) => p.location?.name ?? ""],
          ["Auto delete", (p) => (p.auto_delete ? "yes" : "no")],
        ],
        d.primary_ips,
      ),
      el("h3", {}, "Floating IPs"),
      table(
        [
          ["Name", (f) => [el("strong", {}, f.name), " ", mono(f.id)]],
          ["IP", (f) => mono(f.ip)],
          ["Type", (f) => f.type],
          ["Server", (f) => serverName(f.server)],
          ["Home", (f) => f.home_location.name],
          ["Labels", labels],
        ],
        d.floating_ips,
      ),
    ],
  },
  {
    id: "volumes",
    title: "Volumes",
    count: (d) => d.volumes.length,
    render: (d) =>
      table(
        [
          ["Name", (v) => [el("strong", {}, v.name), " ", mono(v.id)]],
          ["Status", (v) => state(v.status, v.status === "available" ? "good" : "warn")],
          ["Size", (v) => `${v.size} GB`],
          ["Server", (v) => serverName(v.server)],
          ["Location", (v) => v.location.name],
          ["Device", (v) => mono(v.linux_device)],
          ["Labels", labels],
        ],
        d.volumes,
      ),
  },
  {
    id: "firewalls",
    title: "Firewalls",
    count: (d) => d.firewalls.length,
    render: (d) =>
      table(
        [
          ["Name", (f) => [el("strong", {}, f.name), " ", mono(f.id)]],
          [
            "Rules",
            (f) =>
              f.rules.map((r) =>
                el("div", { className: "mono" }, `${r.direction} ${r.protocol}${r.port ? ` ${r.port}` : ""} ${(r.direction === "in" ? r.source_ips : r.destination_ips).join(", ")}`),
              ),
          ],
          [
            "Applied to",
            (f) => f.applied_to.map((a) => chip(a.type === "server" ? serverName(a.server.id) : `label ${a.label_selector.selector}`)),
          ],
          ["Labels", labels],
        ],
        d.firewalls,
      ),
  },
  {
    id: "load-balancers",
    title: "Load balancers",
    count: (d) => d.load_balancers.length,
    render: (d) =>
      table(
        [
          ["Name", (b) => [el("strong", {}, b.name), " ", mono(b.id)]],
          ["Type", (b) => b.load_balancer_type.name],
          ["IPv4", (b) => mono(b.public_net.enabled ? b.public_net.ipv4.ip : "private only")],
          ["Private", (b) => b.private_net.map((n) => chip(`${networkName(n.network)} ${n.ip}`))],
          ["Services", (b) => b.services.map((s) => chip(`${s.protocol} ${s.listen_port} → ${s.destination_port}${s.proxyprotocol ? " proxy" : ""}`))],
          ["Targets", (b) => b.targets.flatMap(targetEntries)],
          ["Algorithm", (b) => b.algorithm.type],
        ],
        d.load_balancers,
      ),
  },
  {
    id: "certificates",
    title: "Certificates",
    count: (d) => d.certificates.length,
    render: (d) =>
      table(
        [
          ["Name", (c) => [el("strong", {}, c.name), " ", mono(c.id)]],
          ["Type", (c) => c.type],
          ["Status", (c) => certificateState(c)],
          ["Domains", (c) => c.domain_names.map(chip)],
          ["Valid until", (c) => time(c.not_valid_after)],
          ["Used by", (c) => c.used_by.map((u) => chip(names.load_balancers?.[u.id] ?? `#${u.id}`))],
        ],
        d.certificates,
      ),
  },
  {
    id: "zones",
    title: "DNS zones",
    count: (d) => d.zones.length,
    render: (d) =>
      table(
        [
          ["Name", (z) => [el("strong", {}, z.name), " ", mono(z.id)]],
          ["Status", (z) => state(z.status, z.status === "ok" ? "good" : "warn")],
          ["Records", (z) => z.record_count],
          ["TTL", (z) => z.ttl],
          ["Labels", labels],
        ],
        d.zones,
      ),
  },
  {
    id: "images",
    title: "Images",
    count: (d) => d.images.length,
    render: (d) =>
      table(
        [
          ["Description", (i) => [el("strong", {}, i.description), " ", mono(i.id)]],
          ["Type", (i) => i.type],
          ["Status", (i) => state(i.status, i.status === "available" ? "good" : "warn")],
          ["From", (i) => i.created_from?.name ?? ""],
          ["Size", (i) => (i.image_size === null ? "" : `${i.image_size} GB`)],
          ["Created", (i) => time(i.created)],
        ],
        d.images,
      ),
  },
  {
    id: "storage-boxes",
    title: "Storage Boxes",
    count: (d) => d.storage_boxes.length,
    render: (d) =>
      table(
        [
          ["Name", (b) => [el("strong", {}, b.name), " ", mono(b.id)]],
          ["Status", (b) => state(b.status, b.status === "active" ? "good" : "warn")],
          ["Type", (b) => b.type],
          ["User", (b) => mono(b.username)],
          ["Sub-accounts", (b) => b.subaccounts.map((s) => chip(s.username))],
          ["Snapshots", (b) => b.snapshots.length],
        ],
        d.storage_boxes,
      ),
  },
  {
    id: "more",
    title: "Keys and groups",
    count: (d) => d.ssh_keys.length + d.placement_groups.length,
    render: (d) => [
      el("h3", {}, "SSH keys"),
      table(
        [
          ["Name", (k) => [el("strong", {}, k.name), " ", mono(k.id)]],
          ["Fingerprint", (k) => mono(k.fingerprint)],
          ["Labels", labels],
        ],
        d.ssh_keys,
      ),
      el("h3", {}, "Placement groups"),
      table(
        [
          ["Name", (g) => [el("strong", {}, g.name), " ", mono(g.id)]],
          ["Type", (g) => g.type],
          ["Servers", (g) => g.servers.map((id) => chip(serverName(id)))],
        ],
        d.placement_groups,
      ),
    ],
  },
  {
    id: "clients",
    title: "Clients and faults",
    count: (d) => d.clients.length + d.faults.length + Object.keys(d.netem).length,
    render: (d) => [
      el("h3", {}, "Internet clients"),
      table(
        [
          ["Name", (c) => el("strong", {}, c.name)],
          ["IPv4", (c) => mono(c.ip)],
          ["IPv6", (c) => mono(c.ip6)],
        ],
        d.clients,
      ),
      el("h3", {}, "Faults"),
      faultsTable(d),
    ],
  },
  // the count: the running actions
  { id: "actions", title: "Actions", count: (d) => d.actions.filter((a) => a.status === "running").length, render: (d) => actionsTable(d.actions) },
];

function targetEntries(target) {
  const statuses = (target.health_status || []).map((h) => h.status);
  const health = (target.health_status || []).map((h) => `${h.listen_port} ${h.status}`);
  const healthTone = statuses.length && statuses.every((s) => s === "healthy") ? "good" : statuses.includes("unhealthy") ? "bad" : "idle";
  let what;
  if (target.type === "server") what = serverName(target.server.id);
  else if (target.type === "ip") what = target.ip.ip;
  else what = `label ${target.label_selector.selector}`;
  const own = el("div", {}, state(what, healthTone), health.length ? el("span", { className: "muted" }, ` ${health.join(", ")}`) : "");
  return [own, ...(target.targets || []).flatMap(targetEntries)];
}

function certificateState(certificate) {
  const status = certificate.status;
  if (!status) return state("uploaded", "good");
  if (status.error) return [state(status.issuance, "bad"), el("div", { className: "muted" }, status.error.message)];
  return state(status.issuance, status.issuance === "completed" ? "good" : "warn");
}

// --- the page ------------------------------------------------------------------------

let latest = null;

function currentSection() {
  const id = location.hash.slice(1);
  return SECTIONS.find((s) => s.id === id) || SECTIONS[0];
}

function render() {
  if (!latest) return;
  const section = currentSection();
  document.getElementById("lab-name").textContent = latest.lab.name;
  document.getElementById("sections").replaceChildren(
    ...SECTIONS.map((s) =>
      el("a", { href: `#${s.id}`, className: s === section ? "current" : "" }, el("span", {}, s.title), el("span", { className: "count" }, s.count(latest))),
    ),
  );
  document.getElementById("content").replaceChildren(el("h2", {}, section.title), ...[section.render(latest)].flat());
}

function connection(text, lost) {
  const node = document.getElementById("connection");
  node.textContent = text;
  node.classList.toggle("lost", lost);
}

async function poll() {
  try {
    const response = await fetch("/ui/api/overview", { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    latest = await response.json();
    names = {
      servers: Object.fromEntries(latest.servers.map((s) => [s.id, s.name])),
      networks: Object.fromEntries(latest.networks.map((n) => [n.id, n.name])),
      load_balancers: Object.fromEntries(latest.load_balancers.map((b) => [b.id, b.name])),
    };
    const resetting = latest.lab.resetting ? " · resetting" : "";
    connection(`updated ${new Date().toLocaleTimeString()}${resetting}`, false);
    render();
  } catch (error) {
    connection(`lab not reachable (${error.message}) – is it up?`, true);
  } finally {
    setTimeout(poll, POLL_MS);
  }
}

window.addEventListener("hashchange", render);
poll();
