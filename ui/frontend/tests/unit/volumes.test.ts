// Unit tests of the volumes actions module: every entry of the create form,
// the actions menu and the detail tabs calls the right endpoint with the
// right body and carries the right confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/volumes";
import { check, id, type Case } from "./resourceTable";

const va = (act: string) => `/volumes/${id}/actions/${act}`;

test.describe("UI-02 volumes", () => {
  const cases: Case[] = [
    {
      name: "create without a server",
      ref: "create",
      input: { name: "data", size: 20, location: "fsn1", labels: { env: "lab" } },
      want: { method: "POST", path: "/volumes", body: { name: "data", size: 20, location: "fsn1", labels: { env: "lab" } } },
      confirm: "none",
    },
    {
      name: "create with a server",
      ref: "create",
      input: { name: "data", size: 20, location: "fsn1", server: 5, automount: true, format: "ext4", labels: {} },
      want: {
        method: "POST",
        path: "/volumes",
        body: { name: "data", size: 20, server: 5, automount: true, format: "ext4", labels: {} },
      },
      confirm: "none",
    },
    {
      name: "resize",
      ref: { menu: "Resize" },
      input: { size: 50 },
      want: { method: "POST", path: va("resize"), body: { size: 50 } },
      confirm: "none",
    },
    {
      name: "attach",
      ref: { menu: "Attach" },
      input: { server: 5, automount: true },
      want: { method: "POST", path: va("attach"), body: { assignee: { id: 5, type: "server" }, automount: true } },
      confirm: "none",
    },
    { name: "detach", ref: { menu: "Detach" }, want: { method: "POST", path: va("detach") }, confirm: "plain" },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: va("change_protection"), body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/volumes/${id}` }, confirm: "typed-name" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
