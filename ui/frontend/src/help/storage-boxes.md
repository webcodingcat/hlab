# Storage Boxes

The storage boxes of this project: network storage reachable over SSH,
SFTP, Samba and WebDAV. Storage boxes can only be managed while the cloud
runs.

- **Create Storage Box** opens a form for the **Location**, the **Type**
  (only the types offered in that location), the **Name**, the
  **Password** (it must follow Hetzner's password rules; a refusal appears
  under the field), the **SSH keys** of this project to install, the
  access settings (SSH, Samba, WebDAV, ZFS, reachable externally) and
  labels.
- The list shows each box's name, username, type, location, usage and
  status and refreshes every few seconds.
- **Delete** in the row menu (`…`) deletes the box and all its data after
  you type its name. With delete protection on, Delete is disabled.
- Clicking a row opens the box's page with these tabs:
  - **Overview** shows the hostname and username with copy buttons, the
    type, the location, the usage of data and snapshots, and the folders
    of the box: type a path or click a folder to descend, **Up** goes back.
  - **Subaccounts** lists the subaccounts. **Create subaccount** asks for
    name, description, home directory, password and access (SSH, Samba,
    WebDAV, read-only, reachable externally); the row menu offers
    **Edit**, **Reset password** and **Delete**.
  - **Snapshots** shows the snapshot plan ("No plan" or the plan in UTC)
    with **Set plan** and **Disable plan**, and the snapshots.
    **Create snapshot** takes an optional description; the row menu
    offers **Edit**, **Roll back** (the confirmation names the newer
    snapshots it deletes) and **Delete**.
  - **Settings** changes the access settings, the delete protection and
    the type (only types offered in the box's location), and resets the
    password to one you enter.
  - **Labels** edits the box's labels.
- The actions menu (`…`) on that page offers **Rename** and **Delete**.

Storage Boxes is an entry of the sidebar's **Cloud** group.
