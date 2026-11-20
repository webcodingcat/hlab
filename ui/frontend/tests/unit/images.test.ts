// Unit tests of the images actions module: every entry of the actions menu
// calls the right endpoint with the right body and carries the right
// confirmation. They need no browser.
import { test } from "@playwright/test";
import * as mod from "../../src/lib/resources/images";
import { check, id, type Case } from "./resourceTable";

test.describe("UI-02 images", () => {
  const cases: Case[] = [
    {
      name: "edit description",
      ref: { menu: "Edit description" },
      input: { description: "before upgrade" },
      want: { method: "PUT", path: `/images/${id}`, body: { description: "before upgrade" } },
      confirm: "none",
    },
    {
      name: "convert to snapshot",
      ref: { menu: "Convert to snapshot" },
      want: { method: "PUT", path: `/images/${id}`, body: { type: "snapshot" } },
      confirm: "none",
    },
    {
      name: "change protection",
      ref: { menu: "Change protection" },
      input: { delete: true },
      want: { method: "POST", path: `/images/${id}/actions/change_protection`, body: { delete: true } },
      confirm: "none",
    },
    { name: "delete", ref: { menu: "Delete" }, want: { method: "DELETE", path: `/images/${id}` }, confirm: "plain" },
  ];
  for (const c of cases) test(c.name, () => check(mod, c));
});
