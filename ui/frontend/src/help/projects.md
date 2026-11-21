# Projects

The start page of HLab: your projects as cards. A project holds its own
resources and API tokens, like a project in the Hetzner Cloud Console.
Projects can only be managed while the cloud runs; while it is stopped,
**Start cloud** starts it.

- Each card shows the project's display name, its name below it and the
  number of resources. Click the number to open a menu with one entry per
  section, such as "1 SSH Key", that opens that section. A project
  without resources offers **+ Create server** instead.
- The search field filters by display name or name. **Sort** orders the
  projects by display name or creation time, the arrow next to it
  reverses the order, and **Grid** and **List** switch the layout. HLab
  keeps the sort and the layout.
- **+ New project** asks for a display name and shows below the field the
  name HLab derives from it: lower case, ä ö ü ß as ae oe ue ss, accents
  dropped, every other run of characters as `-`. The `hlab` command and
  the addresses use the name. A taken name is refused under the field.
- Click a card to open the project's console on **Servers**. Its sidebar
  has the groups **Cloud** (Servers, Volumes, Firewalls) and
  **Networking** (Load Balancers, Networks, IPs, DNS) and the entry
  **Security** (SSH Keys, **API tokens** with the tokens for hcloud,
  OpenTofu and Ansible, and Certificates). The display name in the
  breadcrumb opens a menu to switch to another project, and **Projects**
  leads back to this list. HLab opens the last project you used when it
  starts.
- **Delete** in a card's menu (`…`) deletes the project with all its
  resources; type its display name to confirm.
- **Administration** (the gear in the top bar) holds Server Types, ISOs,
  Settings and Logs; click the gear again to return.
