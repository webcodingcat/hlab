# Floating IPs

The floating IPs of this project, which can move from one server to another.
A floating IP belongs to a home location and is assigned only to servers in
the network zone of that location. Floating IPs can only be managed while
the cloud runs.

- **Create floating IP** opens a form for the **Location** (the home
  location), an optional **Server** to assign the IP to at once (then the
  server's location becomes the home location), the **Protocol** (IPv4 or
  IPv6), the **Name** and labels.
- An IPv4 floating IP is routed to the public IPv4 of its server; configure
  the address in the server's operating system, as at Hetzner. An IPv6
  floating IP is a /64 network that carries no traffic in HLab.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- The row menu (`…`) of an unassigned IP offers **Assign** (to a server in
  the network zone); that of an assigned IP offers **Unassign**, which takes
  it off its server after a confirmation.
- **Delete** in the row menu releases the floating IP after you type its
  name.
  An assigned IP must be unassigned first.
- Clicking a row opens the floating IP's page. **Overview** shows its
  address with a copy button, the protocol, the description (**Edit
  description** changes it), the home location, the server and the reverse
  DNS entries; **Labels** edits its labels; **Protection** turns the delete
  protection on or off.
- The actions menu (`…`) on that page offers **Rename**, **Assign** (to a server in the
  network zone; assigning it to another server moves it), **Unassign**,
  **Reverse DNS** (choose the address and enter a host name; an empty host
  name resets it to the default), **Change protection** and **Delete**.

Floating IPs is a tab of **IPs** in the sidebar's **Networking** group,
beside **Primary IPs**.
