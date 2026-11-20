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
  1. Open the project; the sidebar shows the groups CLOUD (Servers, Volumes, Firewalls) and NETWORKING (Load Balancers, Networks, IPs, DNS), each with a chevron to collapse it, and below a line the entry Security. Collapse NETWORKING, reload the window (F5): it stays collapsed; open it again. Each entry shows its tabs above the page (Servers: Servers, Snapshots, Backups, Placement Groups; IPs: Primary IPs, Floating IPs; DNS: DNS Zones; Security: SSH Keys, API tokens, Certificates). Open the help with "?" in two or three tabs.
  2. Networks: "Create network" `demo-net` with IP range `10.0.0.0/16`; in the labels box type `env=demo` and Enter: a chip appears; click it (accent colour), Delete removes it, type it again. Open the network, add a subnet `10.0.1.0/24` in the Subnets tab (only type cloud is offered).
  3. Firewalls: "Create firewall" `demo-fw` with an inbound TCP rule port 22 from `0.0.0.0/0, ::/0` and one for port 80; leave "Apply to" empty for now.
  4. Servers: create `web1` and `web2` with your SSH key, attached to `demo-net` and with firewall `demo-fw`; wait until both run. Servers > Placement Groups: "Create placement group": Type is a dropdown with only "spread" selected.
  5. Volumes: "Create volume": size field with +/− and a slider (10 to 10240 GB), Name prefilled `volume-1`, Server required (submit without one shows the error under Server), Mount options Automatic | Manual and Filesystem ext4 / xfs always visible, no Location. Create `demo-vol`, 10 GB, server `web1`, Automatic, ext4; open it and check Overview shows the server and the Linux device. Detach it (asks first), then attach it again from the actions menu.
  6. Load Balancers: "Create load balancer" `demo-lb` in the servers' location, network `demo-net`, targets `web1` and `web2` (use private IP), the default HTTP service on port 80; open it and look through Targets, Services (edit the service, the listen port is fixed), Networking and Graphs. Use "Change algorithm" and "Reverse DNS" from the actions menu; the toasts read e.g. "Load balancer demo-lb: Change algorithm".
  7. Firewalls: open `demo-fw`, Resources tab: apply it to a label selector (e.g. `role=web`) and remove it again; change a rule in the Rules tab and "Save rules".
  8. IPs > Floating IPs: "Create floating IP" IPv4 with server `web1`; open it, Unassign (asks first), Assign to `web2`, set a reverse DNS entry. Create an IPv6 floating IP and set the first reverse DNS entry with "New address in …".
  9. DNS: "Add zone" `demo.internal`; in Records add an `A` record `www` pointing at the floating IP, change its TTL, add a second value and remove it; open "Zone file", copy the export.
  10. Servers > Snapshots: from server `web1` take a snapshot in its Snapshots tab; in the Snapshots tab of Servers edit its description and delete it. If a backup exists (Backups tab), "Convert to snapshot" runs without a question.
  11. Own ISO: Administration > ISOs > "Add ISO": Name `demo-iso`, Architecture x86, Source Local file, "Browse…" opens the Windows file dialog, choose any small `.iso` file; "Add". The table shows it as own. In the server `web2`, ISO tab, attach `demo-iso` (listed by its name), then detach it. Edit the ISO (change the name), then Remove it (asks first).
  12. Switch the Windows theme between light and dark once and look at the sidebar, tabs, label chips, lists, forms, detail pages and dialogs.
  13. Delete everything by hand: the zone and the network need their name typed, the rest a plain confirmation; delete the load balancer, IPs, volume, zone and firewall first, then the servers, then the network. Afterwards every section of the project is empty.
- Expected: every step works from the window alone; every action ends in a toast titled `<Type> <name>: <action>`, errors appear at their field; menus offer only what the API accepts (disabled entries name why); the new sections look and behave like Servers and SSH Keys in both themes; at the end nothing of the environment is left in any section.
- Result: open
