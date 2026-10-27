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

// --- controls: the lab's /_lab routes, as the hlab command uses them -------------------

// notice(text, failed): a short message in the header about the last control.
function notice(text, failed) {
  const node = document.getElementById("notice");
  node.textContent = text;
  node.classList.toggle("failed", failed);
}

// send(what, method, path, body): call a /_lab route, report how it went and poll at
// once, so the page shows the change.
async function send(what, method, path, body) {
  notice(`${what} …`, false);
  try {
    const response = await fetch(path, {
      method,
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const answer = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(answer.error?.message || `HTTP ${response.status}`);
    notice(`${what}: done`, false);
  } catch (error) {
    notice(`${what}: ${error.message}`, true);
  }
  poll();
}

function button(text, onclick, className) {
  return el("button", { type: "button", className: className || "", onclick }, text);
}

// field(label, input): a labelled input of a form.
function field(label, input) {
  return el("label", {}, el("span", {}, label), input);
}

// input(name, props): an input; `list` (a datalist's ID) is set as an attribute, the
// only way the DOM takes it.
function input(name, props) {
  const { list, ...rest } = props || {};
  const node = el("input", { name, autocomplete: "off", ...rest });
  if (list) node.setAttribute("list", list);
  return node;
}

function select(name, options) {
  return el("select", { name }, options.map((option) => el("option", { value: option }, option)));
}

// form(title, fields, submit, onsubmit): a form whose values onsubmit gets by name.
function form(title, fields, submit, onsubmit) {
  const node = el("form", { className: "control" }, el("h3", {}, title), el("div", { className: "fields" }, fields, el("button", { type: "submit" }, submit)));
  node.onsubmit = (event) => {
    event.preventDefault();
    onsubmit(Object.fromEntries(new FormData(node)), node);
  };
  return node;
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

// serverControls(server): what `hlab fault pause|cont|dead|alive|isolate` does.
function serverControls(server) {
  const vm = (cmd, what) => button(what, () => send(`${what} ${server.name}`, "POST", `/_lab/vm/${encodeURIComponent(server.name)}/${cmd}`));
  return el(
    "div",
    { className: "buttons" },
    vm("pause", "Pause"),
    vm("cont", "Continue"),
    server.dead ? vm("alive", "Alive") : vm("dead", "Dead"),
    button("Isolate", () => send(`Isolate ${server.name}`, "POST", "/_lab/faults", { type: "isolate", vm: server.name })),
  );
}

// serversTable(data, controls): the servers, with their control buttons if asked.
function serversTable(data, controls) {
  const columns = [
    ["Name", (s) => [el("strong", {}, s.name), " ", mono(s.id)]],
    ["Status", serverState],
    ["Type", (s) => s.server_type.name],
    ["Location", (s) => s.location.name],
    ["IPv4", (s) => mono(s.public_net.ipv4?.ip ?? "–")],
    ["IPv6", (s) => mono(s.public_net.ipv6?.ip ?? "–")],
    ["Private", (s) => s.private_net.map((n) => chip(`${networkName(n.network)} ${n.ip}`))],
    ["Image", (s) => s.image?.name ?? s.image?.description ?? ""],
    ["Labels", labels],
  ];
  if (controls) columns.splice(2, 0, ["Control", serverControls]); // next to the status
  return table(columns, data.servers);
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

// faultsTable(data, controls): every fault; with controls, a button that removes it.
// netem is set per server and network, not per tap: the form below clears it.
function faultsTable(data, controls) {
  const api = data.api_fault;
  const rows = [
    ...data.faults.map((f) => ({ kind: f.type, what: faultText(f), remove: () => send("Remove fault", "POST", "/_lab/faults/remove", f) })),
    ...Object.entries(data.netem).map(([link, args]) => ({ kind: "netem", what: `${link}: ${args}` })),
  ];
  if (api.status && api.count) {
    const remove = () => send("Remove API fault", "POST", "/_lab/api-fault", { ...api, status: 0, count: 0 });
    rows.push({ kind: "api", what: `the next ${api.count} requests answer ${api.status}`, remove });
  }
  if (api.delay_ms) {
    const remove = () => send("Remove API delay", "POST", "/_lab/api-fault", { ...api, delay_ms: 0 });
    rows.push({ kind: "api", what: `every request waits ${api.delay_ms} ms`, remove });
  }
  const columns = [
    ["Fault", (f) => chip(f.kind)],
    ["What", (f) => mono(f.what)],
  ];
  if (controls) columns.push(["", (f) => (f.remove ? button("Remove", f.remove) : "")]);
  return table(columns, rows);
}

// faultControls(): the forms of `hlab fault`, `hlab client add` and `hlab reset`.
function faultControls() {
  const target = (name, placeholder) => input(name, { list: "targets", placeholder, required: true, size: 12 });
  const type = select("type", ["cut", "isolate", "block"]);
  const second = field("and", target("second", "server or client"));
  const net = field("network", select("net", ["all", "public", "private"]));
  const what = field("from", select("what", ["api", "box", "internet"]));
  // only the fields of the chosen type are shown (and sent)
  const showFields = () => {
    second.hidden = net.hidden = type.value !== "cut";
    what.hidden = type.value !== "block";
    second.querySelector("input").required = type.value === "cut";
  };
  type.onchange = showFields;
  const faultForm = form("Add a fault", [field("type", type), field("server or client", target("first", "name")), second, net, what], "Add", (values) => {
    const body = { type: values.type };
    if (values.type === "cut") Object.assign(body, { first: values.first, second: values.second, net: values.net });
    else body.vm = values.first;
    if (values.type === "block") body.what = values.what;
    send(`Add ${values.type}`, "POST", "/_lab/faults", body);
  });
  showFields();
  return el(
    "div",
    { className: "control-grid" },
    faultForm,
    form(
      "Network quality (netem)",
      [field("server or client", target("vm", "name")), field("network", select("net", ["all", "public", "private"])), field("options", input("args", { placeholder: "delay 100ms loss 5% (empty: normal)", size: 28 }))],
      "Set",
      (values) => send(`netem ${values.vm}`, "POST", "/_lab/netem", values),
    ),
    form(
      "API faults",
      [field("status", input("status", { type: "number", min: 0, placeholder: "503" })), field("requests", input("count", { type: "number", min: 0, placeholder: "3" })), field("delay ms", input("delay_ms", { type: "number", min: 0, placeholder: "0" }))],
      "Set",
      (values) => send("API faults", "POST", "/_lab/api-fault", values),
    ),
    form("Internet client", [field("name", input("name", { required: true, placeholder: "alice", maxLength: 12 }))], "Add", (values, node) => {
      send(`Add client ${values.name}`, "POST", "/_lab/clients", values);
      node.reset();
    }),
    el(
      "div",
      { className: "control" },
      el("h3", {}, "Everything"),
      el(
        "div",
        { className: "buttons" },
        button("Clear all faults", () => send("Clear faults", "POST", "/_lab/faults/clear")),
        button(
          "Reset the lab …",
          () => {
            if (confirm("Reset the lab? This deletes everything the APIs created: servers, networks, volumes, Storage Boxes, …")) send("Reset", "POST", "/_lab/reset");
          },
          "danger",
        ),
      ),
    ),
  );
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
  { id: "servers", title: "Servers", count: (d) => d.servers.length, render: (d) => serversTable(d, true) },
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
    controls: faultControls,
    render: (d) => [
      el("h3", {}, "Faults"),
      faultsTable(d, true),
      el("h3", {}, "Internet clients"),
      table(
        [
          ["Name", (c) => el("strong", {}, c.name)],
          ["IPv4", (c) => mono(c.ip)],
          ["IPv6", (c) => mono(c.ip6)],
          ["", (c) => button("Remove", () => send(`Remove client ${c.name}`, "DELETE", `/_lab/clients/${encodeURIComponent(c.name)}`))],
        ],
        d.clients,
      ),
    ],
  },
  // the count: the running actions
  { id: "actions", title: "Actions", count: (d) => d.actions.filter((a) => a.status === "running").length, render: (d) => actionsTable(d.actions) },
  // the live log: not built anew with every poll, new lines are added (appendLog)
  { id: "log", title: "Log", count: () => "", live: true, render: () => logView },
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

// --- the live log ----------------------------------------------------------------------

const LOG_KEEP = 1000; // lines the page keeps
const logView = el("pre", { className: "log" });
let lastLine = 0; // the number of the newest line shown

// appendLog(log): add the new lines; follow them if the view was at its end. The lab
// numbers its lines anew when it restarts; then the page starts again from its first.
function appendLog(log) {
  if (log.newest < lastLine) {
    logView.append(el("div", { className: "muted" }, "— the lab restarted —"));
    lastLine = 0;
  }
  const lines = log.lines.filter((line) => line.n > lastLine); // two polls may overlap
  if (!lines.length) return;
  const atEnd = logView.scrollTop + logView.clientHeight >= logView.scrollHeight - 4;
  for (const line of lines) {
    const tone = / (ERROR|CRITICAL) /.test(line.text) ? "bad" : / WARNING /.test(line.text) ? "warn" : "";
    logView.append(el("div", { className: tone }, line.text));
  }
  while (logView.childElementCount > LOG_KEEP) logView.firstElementChild.remove();
  lastLine = lines[lines.length - 1].n;
  if (atEnd) logView.scrollTop = logView.scrollHeight;
}

// --- the page ------------------------------------------------------------------------

let latest = null;
let shownSection = null;

function currentSection() {
  const id = location.hash.slice(1);
  return SECTIONS.find((s) => s.id === id) || SECTIONS[0];
}

// render(): the navigation and the current section. A section's forms are built only
// when it is entered, so a poll never loses what is typed; a live section stays as it is.
function render() {
  if (!latest) return;
  const section = currentSection();
  const entered = section !== shownSection;
  document.getElementById("lab-name").textContent = latest.lab.name;
  document.getElementById("sections").replaceChildren(
    ...SECTIONS.map((s) =>
      el("a", { href: `#${s.id}`, className: s === section ? "current" : "" }, el("span", {}, s.title), el("span", { className: "count" }, s.count(latest))),
    ),
  );
  document.getElementById("targets").replaceChildren(...[...latest.servers, ...latest.clients].map((t) => el("option", { value: t.name })));
  if (entered) {
    document.getElementById("title").textContent = section.title;
    document.getElementById("controls").replaceChildren(...(section.controls ? [section.controls()] : []));
    shownSection = section; // only now: if the forms failed, the next poll tries again
  }
  if (entered || !section.live) document.getElementById("view").replaceChildren(...[section.render(latest)].flat());
  if (entered && section.live) logView.scrollTop = logView.scrollHeight;
}

function connection(text, lost) {
  const node = document.getElementById("connection");
  node.textContent = text;
  node.classList.toggle("lost", lost);
}

async function fetchJson(path) {
  const response = await fetch(path, { cache: "no-store" });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

let pollTimer = null;

async function poll() {
  clearTimeout(pollTimer);
  try {
    const [overview, log] = await Promise.all([fetchJson("/ui/api/overview"), fetchJson(`/ui/api/log?after=${lastLine}`)]);
    latest = overview;
    names = {
      servers: Object.fromEntries(latest.servers.map((s) => [s.id, s.name])),
      networks: Object.fromEntries(latest.networks.map((n) => [n.id, n.name])),
      load_balancers: Object.fromEntries(latest.load_balancers.map((b) => [b.id, b.name])),
    };
    appendLog(log);
    const resetting = latest.lab.resetting ? " · resetting" : "";
    connection(`updated ${new Date().toLocaleTimeString()}${resetting}`, false);
    render();
  } catch (error) {
    connection(`lab not reachable (${error.message}) – is it up?`, true);
  } finally {
    pollTimer = setTimeout(poll, POLL_MS);
  }
}

window.addEventListener("hashchange", render);
poll();
