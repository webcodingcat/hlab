// Unit tests of the loadBalancers actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/loadBalancers";
import { check, id, type Case } from "./resourceTable";

const la = (act: string) => `/load_balancers/${id}/actions/${act}`;

test.describe("UI-02 load balancers", () => {
  const healthCheck = { protocol: "tcp", port: 80, interval: 15, timeout: 10, retries: 3 };
  const httpCheck = {
    protocol: "http",
    port: 80,
    interval: 15,
    timeout: 10,
    retries: 3,
    http: { domain: "example.com", path: "/health" },
  };
  // The form holds the http fields for every protocol; the request keeps
  // only what the protocol takes.
  const formHttp = {
    sticky_sessions: true,
    cookie_name: "HCLBSTICKY",
    cookie_lifetime: 300,
    certificates: [3],
    redirect_http: true,
  };
  const tcpIn = {
    protocol: "tcp",
    listen_port: 5432,
    destination_port: 5432,
    proxyprotocol: true,
    health_check: healthCheck,
    http: formHttp,
  };
  const tcpOut = { protocol: "tcp", listen_port: 5432, destination_port: 5432, proxyprotocol: true, health_check: healthCheck };
  const httpIn = { protocol: "http", listen_port: 80, destination_port: 8080, proxyprotocol: false, health_check: httpCheck, http: formHttp };
  const httpOut = {
    protocol: "http",
    listen_port: 80,
    destination_port: 8080,
    proxyprotocol: false,
    health_check: httpCheck,
    http: { sticky_sessions: true, cookie_name: "HCLBSTICKY", cookie_lifetime: 300 },
  };
  const httpsIn = { protocol: "https", listen_port: 443, destination_port: 8080, proxyprotocol: false, health_check: httpCheck, http: formHttp };
  const serverTarget = { type: "server", server: { id: 5 }, use_private_ip: true };
  const selectorTarget = { type: "label_selector", label_selector: { selector: "app=web" }, use_private_ip: false };
  const ipTarget = { type: "ip", ip: { ip: "203.0.113.50" } };
  const cases: Case[] = [
    {
      name: "create",
      ref: "create",
      input: {
        name: "lb1",
        load_balancer_type: "lb11",
        location: "fsn1",
        network: 9,
        algorithm: { type: "least_connections" },
        targets: [serverTarget, selectorTarget, ipTarget],
        services: [tcpIn, httpIn, httpsIn],
        public_interface: true,
        labels: { env: "lab" },
      },
      want: {
        method: "POST",
        path: "/load_balancers",
        body: {
          name: "lb1",
          load_balancer_type: "lb11",
          location: "fsn1",
          network: 9,
          algorithm: { type: "least_connections" },
          targets: [serverTarget, selectorTarget, ipTarget],
          services: [tcpOut, httpOut, httpsIn],
          public_interface: true,
          labels: { env: "lab" },
        },
      },
      confirm: "none",
    },
    {
      name: "change type",
      ref: { menu: "Change type" },
      input: { load_balancer_type: "lb21" },
      want: { method: "POST", path: la("change_type"), body: { load_balancer_type: "lb21" } },
      confirm: "plain",
    },
    {
      name: "change algorithm",
      ref: { menu: "Change algorithm" },
      input: { type: "round_robin" },
      want: { method: "POST", path: la("change_algorithm"), body: { type: "round_robin" } },
      confirm: "none",
    },
    {
      name: "enable public interface",
      ref: { menu: "Enable public interface" },
      want: { method: "POST", path: la("enable_public_interface") },
      confirm: "none",
    },
    {
      name: "disable public interface",
      ref: { menu: "Disable public interface" },
      want: { method: "POST", path: la("disable_public_interface") },
      confirm: "plain",
    },
    {
      name: "reverse DNS set",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.40", dns_ptr: "lb.example.com" },
      want: { method: "POST", path: la("change_dns_ptr"), body: { ip: "203.0.113.40", dns_ptr: "lb.example.com" } },
      confirm: "none",
    },
    {
      name: "reverse DNS reset",
      ref: { menu: "Reverse DNS" },
      input: { ip: "203.0.113.40", dns_ptr: "" },
      want: { method: "POST", path: la("change_dns_ptr"), body: { ip: "203.0.113.40", dns_ptr: null } },
      confirm: "none",
    },
    {
      name: "add service",
      ref: { tab: "add_service" },
      input: httpIn,
      want: { method: "POST", path: la("add_service"), body: httpOut },
      confirm: "none",
    },
    {
      name: "update service",
      ref: { tab: "update_service" },
      input: tcpIn,
      want: { method: "POST", path: la("update_service"), body: tcpOut },
      confirm: "none",
    },
    {
      name: "delete service",
      ref: { tab: "delete_service" },
      input: { listen_port: 80 },
      want: { method: "POST", path: la("delete_service"), body: { listen_port: 80 } },
      confirm: "plain",
    },
    {
      name: "add target",
      ref: { tab: "add_target" },
      input: serverTarget,
      want: { method: "POST", path: la("add_target"), body: serverTarget },
      confirm: "none",
    },
    {
      name: "remove target",
      ref: { tab: "remove_target" },
      input: serverTarget,
      want: { method: "POST", path: la("remove_target"), body: { type: "server", server: { id: 5 } } },
      confirm: "plain",
    },
    {
      name: "attach network",
      ref: { tab: "attach_to_network" },
      input: { network: 9, ip: "10.0.1.10" },
      want: { method: "POST", path: la("attach_to_network"), body: { network: 9, ip: "10.0.1.10" } },
      confirm: "none",
    },
    {
      name: "detach network",
      ref: { tab: "detach_from_network" },
      input: { network: 9 },
      want: { method: "POST", path: la("detach_from_network"), body: { network: 9 } },
      confirm: "plain",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: la("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/load_balancers/${id}` }, confirm: "typed-name" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
