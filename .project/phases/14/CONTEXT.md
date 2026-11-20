# Phase 14 context

Confirmed at the start of phase 14. The planner's assumptions stand with these corrections and answers. No research; phases/11/RESEARCH.md stands for the Hetzner console forms.

## Corrections

- Volume create: without a server the form needs a Location (the API requires location or server); Location stands after Name and disappears once a server is chosen, then the server's location applies; mount option and filesystem as assumed.
- Images: converting a backup to a snapshot exists in HLab (`PUT /images/{id}` with `type: snapshot`); it is a fixed entry in a backup's actions menu, without confirmation.
- Load balancer actions menu also holds Change type, Change algorithm, Enable/Disable public interface and Reverse DNS (`change_dns_ptr`); Disable public interface and Change type get a plain confirmation.
- Firewall: apply to and remove from servers and label selectors (`apply_to_resources`/`remove_from_resources` with `label_selector`), in the apply tab and in the create form.
- Network: subnets offer only type `cloud` (HLab answers `vswitch` with 422); `expose_routes_to_vswitch` is not shown (no effect); Change IP range has a plain confirmation and its help text says only growing works and not while servers are attached.
- DNS: the zone actions menu has Change default TTL (`change_ttl`); per RRSet: change TTL, add and remove records, labels and protection, through the routes under `/zones/{id}/rrsets/{name}/{type}`; the SOA is managed by HLab, only its TTL can be changed, so it is not editable in the table.
- Primary IP create takes a Location, not a datacenter (HLab has no datacenter field), plus `auto_delete` and an optional server to assign; `auto_delete` toggles in the detail via PUT; actions Assign, Unassign (plain confirmation) and Reverse DNS. Floating IPs the same, with a home location or a server at create.
- Managed certificate: the help text says the domains must lie under a primary zone of this project and HLab's CA issues the certificate; the error `dns_zone_not_found` shows in the toast.

## Answers

- Q1 (UI-02 e2e depth): per section one short flow without a booted server: create, change a label, delete; ISOs only the list. Images and everything needing a running server (attach volume, LB targets, assign IP, firewall on a server) are proven by the unit tables of the requests and by hand in UI-09. All specs share one project. The run's time limit is about twice the first full run, not raised by a flat amount.
