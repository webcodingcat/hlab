# Projects

The start page of HLab: the list of your projects. A project holds its own
resources and API tokens, like a project in the Hetzner Cloud Console.
Projects can only be managed while the cloud runs; while it is stopped,
**Start cloud** starts it.

- **Create project** adds a project.
- The search field above the table filters the list by name.
- Click a project to open its console on **Servers**. Its sidebar lists
  the sections of the project: **Servers**, **SSH Keys** and
  **API tokens**, which holds the tokens for hcloud, OpenTofu and
  Ansible. The name in the breadcrumb opens a menu to switch to another
  project, and **Projects** leads back to this list. HLab opens the last
  project you used when it starts.
- **Delete** in a project's row menu (`…`) deletes the project with all
  its resources; type its name to confirm.
- **Administration** (the gear in the top bar) holds Server Types,
  Settings and Logs; click the gear again to return.
