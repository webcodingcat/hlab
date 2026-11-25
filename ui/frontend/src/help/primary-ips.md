# Primary IPs

The primary IPs of this project, the public addresses of its servers. A
primary IP belongs to a location and is assigned only to a powered-off
server in that location. Primary IPs can only be managed while the cloud
runs.

- **Create primary IP** opens a form for the **Name**, the **Location**,
  the **Protocol** (IPv4 or IPv6), an optional **Server** to assign the IP
  to at once (powered-off servers of the location without a public address
  of that protocol), **Auto delete** (the IP is deleted together with its
  server) and labels.
- An IPv6 primary IP is a /64 network that carries no traffic in HLab.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- The row menu (`…`) of an unassigned IP offers **Assign** (to a
  powered-off server of the location); that of an assigned IP offers
  **Unassign**, which takes it off its server after a confirmation; the
  server must be powered off. An IP held by a
  load balancer is released by deleting the load balancer.
- **Delete** in the row menu releases the primary IP after you type its
  name.
  An assigned IP must be unassigned first.
- Clicking a row opens the primary IP's page. **Overview** shows its
  address with a copy button, the protocol, the location, the assignee,
  the **Auto delete** switch and the reverse DNS entries; **Labels** edits
  its labels; **Protection** turns the delete protection on or off (not
  while auto delete is on).
- The actions menu (`…`) on that page offers **Rename**, **Assign** (to a powered-off
  server of the location), **Unassign**, **Reverse DNS** (choose the
  address, or for IPv6 a new address in the network, and enter a host name;
  an empty host name resets it to the default), **Change protection** and
  **Delete**.

Primary IPs is a tab of **IPs** in the sidebar's **Networking** group,
beside **Floating IPs**.
