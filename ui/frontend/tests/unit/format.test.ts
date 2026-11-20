// Unit tests of the toast title builder: the title of an action on a named
// resource and the resource type of the API path the console sends. They
// need no browser.
import { test, expect } from "@playwright/test";
import { actionTitle, resourceType } from "../../src/lib/format";

test.describe("UI-02 action titles", () => {
  const titles: { name: string; type: string; resource: string; action: string; want: string }[] = [
    { name: "network delete", type: "Network", resource: "Test", action: "Delete", want: "Network Test: Delete" },
    { name: "network create", type: "Network", resource: "web", action: "Create", want: "Network web: Create" },
    {
      name: "record change TTL",
      type: "Record",
      resource: "www A",
      action: "Change TTL",
      want: "Record www A: Change TTL",
    },
  ];
  for (const c of titles) {
    test(`actionTitle: ${c.name}`, () => {
      expect(actionTitle(c.type, c.resource, c.action)).toBe(c.want);
    });
  }

  const paths: { path: string; want: string }[] = [
    { path: "/servers", want: "Server" },
    { path: "/servers/1", want: "Server" },
    { path: "/servers/1/actions/poweron", want: "Server" },
    { path: "/volumes", want: "Volume" },
    { path: "/volumes/3", want: "Volume" },
    { path: "/volumes/3/actions/resize", want: "Volume" },
    { path: "/firewalls", want: "Firewall" },
    { path: "/firewalls/2", want: "Firewall" },
    { path: "/firewalls/2/actions/set_rules", want: "Firewall" },
    { path: "/images/4", want: "Image" },
    { path: "/images/4/actions/change_protection", want: "Image" },
    { path: "/placement_groups", want: "Placement group" },
    { path: "/placement_groups/8", want: "Placement group" },
    { path: "/load_balancers", want: "Load balancer" },
    { path: "/load_balancers/9", want: "Load balancer" },
    { path: "/load_balancers/9/actions/add_service", want: "Load balancer" },
    { path: "/networks", want: "Network" },
    { path: "/networks/5", want: "Network" },
    { path: "/networks/5/actions/add_subnet", want: "Network" },
    { path: "/primary_ips", want: "Primary IP" },
    { path: "/primary_ips/6", want: "Primary IP" },
    { path: "/primary_ips/6/actions/assign", want: "Primary IP" },
    { path: "/floating_ips", want: "Floating IP" },
    { path: "/floating_ips/7", want: "Floating IP" },
    { path: "/floating_ips/7/actions/unassign", want: "Floating IP" },
    { path: "/zones", want: "Zone" },
    { path: "/zones/1", want: "Zone" },
    { path: "/zones/1/actions/change_ttl", want: "Zone" },
    { path: "/zones/1/rrsets", want: "Record" },
    { path: "/zones/1/rrsets/www/A", want: "Record" },
    { path: "/zones/1/rrsets/www/A/actions/change_ttl", want: "Record" },
    { path: "/certificates", want: "Certificate" },
    { path: "/certificates/2", want: "Certificate" },
    { path: "/certificates/2/actions/retry", want: "Certificate" },
    { path: "/ssh_keys", want: "SSH key" },
    { path: "/ssh_keys/3", want: "SSH key" },
  ];
  for (const c of paths) {
    test(`resourceType: ${c.path}`, () => {
      expect(resourceType(c.path)).toBe(c.want);
    });
  }
});
