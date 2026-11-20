# Firewalls

The firewalls of this project, whose rules filter the traffic of the
servers they apply to. Firewalls can only be managed while the cloud runs.

- **Create firewall** opens a form for the rules, the servers and label
  selectors the firewall applies to, labels and the **Name**.
- A rule has a **Protocol** (TCP, UDP, ICMP, ESP or GRE), a **Port** for TCP
  and UDP (`80`, a range `1024-2048` or `any`), its IPs and an optional
  description. Inbound rules name **Source IPs**, outbound rules
  **Destination IPs**: CIDRs such as `0.0.0.0/0` or `10.0.0.0/24`,
  separated by commas or line breaks. Traffic that no inbound rule allows
  is dropped; without outbound rules all outgoing traffic is allowed.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) or in the actions menu on a firewall's
  page deletes the firewall after a confirmation. That actions menu also
  offers **Rename**.
- On a firewall's page, the **Rules** tab edits the rules; **Save rules**
  replaces all of them at once.
- The **Resources** tab lists the servers and label selectors the firewall
  applies to, with the servers each selector matches. **Apply to server**
  and **Apply to label selector** add one; **Remove** takes one away. A
  firewall applied through a label selector is removed here, not in a
  server's tab.

Firewalls is an entry of the sidebar's **Cloud** group.
