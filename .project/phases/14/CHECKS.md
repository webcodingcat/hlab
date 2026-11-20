# Phase 14 manual checks

Phase 14 stops for a manual check: the example environment of the success
criterion is built and deleted by hand in the console, which also walks
through the steps the end-to-end tests leave out because they need a
running server (attaching a volume, load balancer targets, assigning IPs,
applying a firewall, images).

## UI-09 The example environment can be created and deleted by hand in the console

- Start: `task build`, then restart the dev service (`Restart-Service hlab-dev`, elevated) and start `bin\hlab-tray.exe`; open the window from the tray icon and start the cloud if it is stopped.
- Setup: a project with an API token, e.g. `bin\hlab.exe project create demo`, and an SSH public key at hand (e.g. the content of `~\.ssh\id_ed25519.pub`). Claude can prepare the project and the SSH key in it on request.
- Steps:
  1. Open the project; the sidebar shows Servers, Volumes, Load Balancers, Networks, Firewalls, Floating IPs, Primary IPs, DNS, Certificates, Placement Groups, Images, ISOs, SSH Keys and API tokens. Open the help with "?" in two or three sections.
  2. Networks: "Create network" `demo-net` with IP range `10.0.0.0/16`; open it, add a subnet `10.0.1.0/24` in the Subnets tab (only type cloud is offered).
  3. Firewalls: "Create firewall" `demo-fw` with an inbound TCP rule port 22 from `0.0.0.0/0, ::/0` and one for port 80; leave "Apply to" empty for now.
  4. Servers: create `web1` and `web2` with your SSH key, attached to `demo-net` and with firewall `demo-fw`; wait until both run.
  5. Volumes: "Create volume" `demo-vol`, 10 GB, choose server `web1` with automatic mount; open it and check Overview shows the server and the Linux device. Detach it (asks first), then attach it again from the actions menu.
  6. Load Balancers: "Create load balancer" `demo-lb` in the servers' location, network `demo-net`, targets `web1` and `web2` (use private IP), the default HTTP service on port 80; open it and look through Targets, Services (edit the service, the listen port is fixed), Networking and Graphs. Use "Change algorithm" and "Reverse DNS" from the actions menu.
  7. Firewalls: open `demo-fw`, Resources tab: apply it to a label selector (e.g. `role=web`) and remove it again; change a rule in the Rules tab and "Save rules".
  8. Floating IPs: "Create floating IP" IPv4 with server `web1`; open it, Unassign (asks first), Assign to `web2`, set a reverse DNS entry. Create an IPv6 floating IP and set the first reverse DNS entry with "New address in …".
  9. DNS: "Add zone" `demo.internal`; in Records add an `A` record `www` pointing at the floating IP, change its TTL, add a second value and remove it; open "Zone file", copy the export.
  10. Images: from server `web1` take a snapshot in its Snapshots tab; in Images edit its description and delete it. If a backup exists, "Convert to snapshot" runs without a question.
  11. Switch the Windows theme between light and dark once and look at lists, forms, detail pages and dialogs of the new sections.
  12. Delete everything by hand: the zone and the network need their name typed, the rest a plain confirmation; delete the load balancer, IPs, volume, zone and firewall first, then the servers, then the network. Afterwards every section of the project is empty.
- Expected: every step works from the window alone; every action ends in a toast, errors appear at their field; menus offer only what the API accepts (disabled entries name why); the new sections look and behave like Servers and SSH Keys in both themes; at the end nothing of the environment is left in any section.
- Result: open; waits for the change in PLAN.md "Findings from manual testing" (grouped sidebar with tabs, ISOs in the Administration). Step 1 then reads: the sidebar shows Cloud (Servers, Volumes, Firewalls), Networking (Load Balancers, Networks, IPs, DNS) and Security; snapshots and backups are tabs under Servers (step 10), floating IPs a tab under IPs (step 8).
