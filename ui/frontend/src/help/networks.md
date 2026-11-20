# Networks

The private networks of this project, which connect servers and load
balancers. Networks can only be managed while the cloud runs.

- **Create network** opens a form for the **Name**, the **IP range** (a
  private IPv4 range in CIDR notation, for example `10.0.0.0/16`) and
  labels. Subnets and routes are added on the network's page.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the network after you type its
  name; attached servers and load balancers are detached from it.
- Clicking a row opens the network's page. **Overview** shows its IP
  range; **Subnets** lists the subnets with **Add subnet** (a network zone
  and a range inside the network) and **Delete** per subnet; **Routes**
  lists the routes with **Add route** (a destination and a gateway inside
  the network) and **Delete** per route; **Resources** lists the attached
  servers and load balancers, each linking to its page; **Labels** edits
  its labels; **Protection** turns the delete protection on or off.
- The actions menu (`…`) on a network's page offers **Rename**, **Change IP range**,
  **Change protection** and **Delete**.
- **Change IP range** only grows the range: the new range must contain the
  old one. It is refused while servers are attached to the network.
- Subnets are always of type cloud and lie in one network zone.

Networks is an entry of the sidebar's **Networking** group.
