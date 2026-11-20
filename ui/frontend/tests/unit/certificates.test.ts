// Unit tests of the certificates actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/certificates";
import { check, id, type Case } from "./resourceTable";

test.describe("UI-02 certificates", () => {
  const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";
  const key = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n";
  const cases: Case[] = [
    {
      name: "create uploaded",
      ref: "create",
      input: { type: "uploaded", name: "web", certificate: pem, private_key: key, labels: { env: "lab" } },
      want: {
        method: "POST",
        path: "/certificates",
        body: { type: "uploaded", name: "web", certificate: pem, private_key: key, labels: { env: "lab" } },
      },
      confirm: "none",
    },
    {
      name: "create managed",
      ref: "create",
      input: { type: "managed", name: "web", domain_names: ["example.com", "www.example.com"], labels: {} },
      want: {
        method: "POST",
        path: "/certificates",
        body: { type: "managed", name: "web", domain_names: ["example.com", "www.example.com"], labels: {} },
      },
      confirm: "none",
    },
    {
      name: "retry",
      ref: { menu: "Retry" },
      want: { method: "POST", path: `/certificates/${id}/actions/retry` },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/certificates/${id}` }, confirm: "plain" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
