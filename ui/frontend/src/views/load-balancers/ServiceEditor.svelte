<script module lang="ts">
  import type { Input } from "../../lib/resourceActions";
  import type { LoadBalancerService } from "./Section.svelte";

  /** ServiceForm holds every field of a service; serviceInput sends only what applies. */
  export type ServiceForm = {
    protocol: "tcp" | "http" | "https";
    listen_port: number;
    destination_port: number;
    proxyprotocol: boolean;
    health_check: {
      protocol: "tcp" | "http";
      port: number;
      interval: number;
      timeout: number;
      retries: number;
      http: { domain: string; path: string; tls: boolean };
    };
    http: {
      sticky_sessions: boolean;
      cookie_name: string;
      cookie_lifetime: number;
      certificates: number[];
      redirect_http: boolean;
    };
  };

  /** newService is an http service on port 80 with an http health check. */
  export function newService(): ServiceForm {
    return {
      protocol: "http",
      listen_port: 80,
      destination_port: 80,
      proxyprotocol: false,
      health_check: { protocol: "http", port: 80, interval: 15, timeout: 10, retries: 3, http: { domain: "", path: "/", tls: false } },
      http: { sticky_sessions: false, cookie_name: "HCLBSTICKY", cookie_lifetime: 300, certificates: [], redirect_http: false },
    };
  }

  /** serviceForm turns a service of the API into the form; missing http blocks get newService's values. */
  export function serviceForm(s: LoadBalancerService): ServiceForm {
    const d = newService();
    const hc = s.health_check;
    return {
      protocol: s.protocol,
      listen_port: s.listen_port,
      destination_port: s.destination_port,
      proxyprotocol: s.proxyprotocol,
      health_check: {
        protocol: hc.protocol,
        port: hc.port,
        interval: hc.interval,
        timeout: hc.timeout,
        retries: hc.retries,
        http: hc.http ? { domain: hc.http.domain ?? "", path: hc.http.path, tls: hc.http.tls } : d.health_check.http,
      },
      http: s.http ? { ...s.http, certificates: [...s.http.certificates] } : d.http,
    };
  }

  /**
   * serviceInput is the service as the actions module takes it: a tcp health
   * check without its http block, an empty health check domain as null. The
   * module drops the http fields the protocol does not take.
   */
  export function serviceInput(s: ServiceForm): Input {
    const hc = s.health_check;
    const check: Input = { protocol: hc.protocol, port: hc.port, interval: hc.interval, timeout: hc.timeout, retries: hc.retries };
    if (hc.protocol === "http") {
      const domain = hc.http.domain.trim();
      check.http = { domain: domain === "" ? null : domain, path: hc.http.path, tls: hc.http.tls };
    }
    return {
      protocol: s.protocol,
      listen_port: s.listen_port,
      destination_port: s.destination_port,
      proxyprotocol: s.proxyprotocol,
      health_check: check,
      http: { ...s.http, certificates: [...s.http.certificates] },
    };
  }
</script>

<script lang="ts">
  import Field from "../../lib/Field.svelte";

  // One load balancer service: protocol and ports, proxy protocol, the
  // health check and, for http and https, sticky sessions; https also takes
  // certificates and the redirect from http. Used by the create form and the
  // Services tab. fixedPort keeps the listen port, which names the service
  // being edited.
  type Props = { service: ServiceForm; certificates: { id: number; name: string }[]; fixedPort?: boolean };

  let { service = $bindable(), certificates, fixedPort = false }: Props = $props();

  // A new protocol brings its usual listen port and health check protocol.
  function setProtocol(p: ServiceForm["protocol"]) {
    service.protocol = p;
    if (!fixedPort && service.protocol === "https") service.listen_port = 443;
    else if (!fixedPort && service.protocol === "http") service.listen_port = 80;
    service.health_check.protocol = service.protocol === "tcp" ? "tcp" : "http";
  }

  function toggleCertificate(id: number, on: boolean) {
    const rest = service.http.certificates.filter((c) => c !== id);
    service.http.certificates = on ? [...rest, id] : rest;
  }
</script>

<div class="service">
  <Field label="Protocol">
    {#snippet children(c)}
      <select id={c.id} aria-describedby={c.describedby} bind:value={() => service.protocol, setProtocol}>
        <option value="tcp">TCP</option>
        <option value="http">HTTP</option>
        <option value="https">HTTPS</option>
      </select>
    {/snippet}
  </Field>
  <div class="row">
    <Field label="Listen port">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} type="number" min="1" max="65535" disabled={fixedPort} bind:value={service.listen_port} />
      {/snippet}
    </Field>
    <Field label="Destination port">
      {#snippet children(c)}
        <input id={c.id} aria-describedby={c.describedby} type="number" min="1" max="65535" bind:value={service.destination_port} />
      {/snippet}
    </Field>
  </div>
  <label class="check"><input type="checkbox" bind:checked={service.proxyprotocol} />Proxy protocol</label>

  {#if service.protocol !== "tcp"}
    <label class="check"><input type="checkbox" bind:checked={service.http.sticky_sessions} />Sticky sessions</label>
    {#if service.http.sticky_sessions}
      <div class="row">
        <Field label="Cookie name">
          {#snippet children(c)}
            <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={service.http.cookie_name} />
          {/snippet}
        </Field>
        <Field label="Cookie lifetime (s)">
          {#snippet children(c)}
            <input id={c.id} aria-describedby={c.describedby} type="number" min="30" max="86400" bind:value={service.http.cookie_lifetime} />
          {/snippet}
        </Field>
      </div>
    {/if}
  {/if}
  {#if service.protocol === "https"}
    <fieldset>
      <legend>Certificates</legend>
      {#each certificates as cert (cert.id)}
        <label class="check">
          <input
            type="checkbox"
            checked={service.http.certificates.includes(cert.id)}
            onchange={(e) => toggleCertificate(cert.id, e.currentTarget.checked)}
          />{cert.name}
        </label>
      {:else}
        <p class="muted">This project has no certificates.</p>
      {/each}
    </fieldset>
    <label class="check"><input type="checkbox" bind:checked={service.http.redirect_http} />Redirect HTTP to HTTPS</label>
  {/if}

  <fieldset>
    <legend>Health check</legend>
    <div class="row">
      <Field label="Health check protocol">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={service.health_check.protocol}>
            <option value="tcp">TCP</option>
            <option value="http">HTTP</option>
          </select>
        {/snippet}
      </Field>
      <Field label="Health check port">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="1" max="65535" bind:value={service.health_check.port} />
        {/snippet}
      </Field>
    </div>
    <div class="row">
      <Field label="Interval (s)">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="3" max="60" bind:value={service.health_check.interval} />
        {/snippet}
      </Field>
      <Field label="Timeout (s)">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="1" max="60" bind:value={service.health_check.timeout} />
        {/snippet}
      </Field>
      <Field label="Retries">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} type="number" min="1" max="5" bind:value={service.health_check.retries} />
        {/snippet}
      </Field>
    </div>
    {#if service.health_check.protocol === "http"}
      <div class="row">
        <Field label="Health check domain" hint="Optional Host header.">
          {#snippet children(c)}
            <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={service.health_check.http.domain} />
          {/snippet}
        </Field>
        <Field label="Health check path">
          {#snippet children(c)}
            <input id={c.id} aria-describedby={c.describedby} autocomplete="off" bind:value={service.health_check.http.path} />
          {/snippet}
        </Field>
      </div>
      <label class="check"><input type="checkbox" bind:checked={service.health_check.http.tls} />Health check over TLS</label>
    {/if}
  </fieldset>
</div>

<style>
  .service {
    display: flex;
    flex-direction: column;
  }
  .row {
    display: flex;
    gap: var(--s3);
  }
  .row > :global(*) {
    flex: 1;
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    margin: 0 0 var(--s4);
    padding: 0;
    border: none;
  }
  legend {
    margin-bottom: var(--s1);
    font-weight: 600;
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--s2);
    margin-bottom: var(--s3);
  }
  .muted {
    margin: 0;
    color: var(--muted);
  }
  select,
  input:not([type="checkbox"]) {
    width: 100%;
  }
</style>
