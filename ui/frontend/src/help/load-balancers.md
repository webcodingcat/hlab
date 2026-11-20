# Load Balancers

The load balancers of this project, which spread traffic over their targets.
Load balancers can only be managed while the cloud runs.

- **Create load balancer** opens a form for the **Location**, the **Type**,
  an optional **Network** and the public interface, the **Targets**, the
  **Services**, the **Algorithm**, labels and the **Name**.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the load balancer and its public
  IPs after a confirmation.
- Clicking a row opens the load balancer's page:
  - **Overview** shows its public IPs with a copy button and their reverse
    DNS, whether the public interface is enabled, the type, the algorithm,
    the location and how many targets are healthy.
  - **Networking** lists its private networks with its IP in each;
    **Attach to network** adds it to a network with a subnet in its network
    zone (the IP is optional), **Detach** removes it after a confirmation.
  - **Graphs** shows the open connections, the connections and requests per
    second and the bandwidth over the last hour, 24 hours or 7 days. HLab
    keeps the samples 24 hours.
  - **Labels** edits its labels; **Protection** turns the delete protection
    on or off.
- The actions menu (`…`) on that page offers **Change type** (after a
  confirmation), **Change algorithm** (round robin or least connections),
  **Enable public interface**, **Disable public interface** (after a
  confirmation), **Reverse DNS** (choose the IP and enter a host name; an
  empty name resets it to the default), **Change protection** and
  **Delete**.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
