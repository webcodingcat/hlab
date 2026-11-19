# Phase 06 test map

Subtest names carry the requirement ID, in a group named after the
component. Unit tests of the firewalls module go through `apitest`
(responses validated against the frozen OpenAPI description) with
`platformtest.Fake` and a recording agent push; servers are created through
`POST /servers` of the servers module mounted beside the module under test,
with `srv.SetFirewalls(fw)` and `srv.OnDelete(fw.ServerDeleted)`. The group
`TestFirewalls` lives in internal/resources/firewalls/firewalls_test.go and
also runs the rows of rules_test.go, actions_test.go and contribute_test.go.
Agent rows run in `TestAgent` (group in internal/agent/metadata_test.go,
rows in firewall_test.go); `CompileFirewalls` has no build tag, so they run
under Windows `go test`.

End-to-end subtests live under `TestE2E` in `test/e2e` (build tag `e2e`,
registered in e2e_test.go, run in parallel). FW-02 and FW-03 each use one
own server of the smallest x86 type (`ownServerImage`) with user_data that
starts `python3 -m http.server 80`; all probes run from the host; every wait
is a condition with a deadline. A blocked port is a connect that fails
within a short deadline after the action has ended `success`.

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| FW-01 | `CheckRules` accepts tcp/udp with `80`, `1000-2000`, `any`, icmp/esp/gre without port, IPv4 and IPv6 CIDRs, and returns the rules and IPs in the given order with IPv6 in RFC 5952 form and the other direction's list `[]`; each refusal answers `invalid_input` on its field: unknown direction or protocol, tcp/udp without port, port on icmp, port 0, 65536, `2000-1000`, `8a`, `in` without `source_ips`, `out` without `destination_ips`, an address without mask, a CIDR with host bits, 101 IPs, 51 rules, a description of 256 characters | unit | internal/resources/firewalls/rules_test.go: TestFirewalls/FW-01/rules | 06-02 |
| FW-01 | `POST /firewalls` with name, labels and rules answers 201 with the firewall (rules as sent, `applied_to` []) and a `set_firewall_rules` action that ends `success` and is readable at `/actions/{id}` and `/actions?id=`; an invalid rule answers 422 `invalid_input` on `rules[0].port` and stores nothing; list, get, PUT (name, labels) work; DELETE of an unapplied firewall answers 204 | unit | internal/resources/firewalls/firewalls_test.go: TestFirewalls/FW-01/crud | 06-05 |
| FW-01 | `apply_to_resources` with a server answers 201 with an `apply_firewall` action naming server and firewall that ends `success` after the agent push; the firewall's `applied_to` and the server's `public_net.firewalls` (`{id, status: applied}`) show it; again: 422 `firewall_already_applied`; an unknown server: 404 `firewall_resource_not_found`; a server without public IPv4: 422 `private_net_only_server`; a sixth firewall on a server: 403 `resource_limit_exceeded`; `remove_from_resources` answers 201 with `remove_firewall` and clears both; a server not applied: 404 `firewall_resource_not_found`; a failed agent push ends the action `error` | unit | internal/resources/firewalls/actions_test.go: TestFirewalls/FW-01/apply_remove | 06-06 |
| FW-01 | `POST /firewalls` with `apply_to` of a server and a label selector returns `set_firewall_rules` and the apply actions; an unknown server answers 422 `firewall_resource_not_found`, the same server twice 422 `server_already_added`; the selector entry lists `applied_to_resources` of the matching servers with a public IPv4 only; removing a server attached only by the selector answers 422 `firewall_managed_by_label_selector`; `remove_from` with the selector removes it | unit | internal/resources/firewalls/actions_test.go: TestFirewalls/FW-01/label_selector | 06-06 |
| FW-01 | `set_rules` replaces the rules (order kept) and answers 201 with `set_firewall_rules` plus one `apply_firewall` per applied server; an invalid rule answers 422 `invalid_input` and keeps the old rules | unit | internal/resources/firewalls/actions_test.go: TestFirewalls/FW-01/set_rules | 06-06 |
| FW-01 | `DELETE` of a firewall applied to a server or a selector answers 422 `resource_in_use` with a message containing `is still in use`; after removal it answers 204; deleting the server removes it from `applied_to` | unit | internal/resources/firewalls/actions_test.go: TestFirewalls/FW-01/delete_in_use | 06-06 |
| FW-01 | `POST /servers` with `firewalls: [{firewall: id}]` creates the server with the firewall in `public_net.firewalls`, an `apply_firewall` action in `next_actions` and the server in the firewall's `applied_to`; an unknown firewall answers 404 `not_found` and creates no server | unit | internal/resources/firewalls/actions_test.go: TestFirewalls/FW-01/server_create | 06-06 |
| FW-02 | `CompileFirewalls`: a server with two firewalls gets the union of their `in` rules with parsed ports (`80`, `1000-2000`, `any`) and IPv4 prefixes, its addresses are its public IPv4 plus the floating IP routed to it; a server without firewall has no filter; a rule with only IPv6 prefixes is dropped, never widened to all sources | unit | internal/agent/firewall_test.go: TestAgent/FW-02 | 06-04 |
| FW-02 | with a firewall allowing only tcp 22 in, SSH from the host to the server works and port 80 does not connect; after `remove_from_resources` port 80 answers | e2e | test/e2e/firewall_test.go: TestE2E/FW-02 | 06-08 |
| FW-03 | `CompileFirewalls`: without any `out` rule `RestrictOut` is false; when one of the server's firewalls has an `out` rule it is true and `Out` holds the union of the `out` rules of all its firewalls | unit | internal/agent/firewall_test.go: TestAgent/FW-03 | 06-04 |
| FW-03 | with a firewall allowing tcp 22 in and only tcp 443 out to `0.0.0.0/0`, the server fetches an https URL from the internet, a plain http URL fails, and SSH from the host still works | e2e | test/e2e/firewall_test.go: TestE2E/FW-03 | 06-08 |
| FW-04 | `CompileFirewalls` on a document whose firewalled server is also a member of a private network (IP and alias IP) gives a filter whose addresses are only its public IPv4 and floating IP, and a server that is only in a private network gets no filter | unit | internal/agent/firewall_test.go: TestAgent/FW-04 | 06-04 |
| FW-05 | with a firewall applied to `role=web`, a server without the label has no firewall in the pushed document; after `PUT /servers/{id}` sets `role=web` a new push lists the firewall in the server's `FirewallIDs` and its rules in `Firewalls`, and the server's `public_net.firewalls` shows it; removing the label drops it from the next push | unit | internal/resources/firewalls/contribute_test.go: TestFirewalls/FW-05 | 06-07 |
| API-06 | `/firewalls` filters by `name` and sorts by `id:desc`, one case | unit | internal/resources/conventions_test.go: TestResources/API-06 | 06-08 |
| API-11 | a second firewall with the same name in the project answers `uniqueness_error`, one case | unit | internal/resources/conventions_test.go: TestResources/API-11 | 06-08 |
