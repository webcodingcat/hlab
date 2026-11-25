# Volumes

The block storage volumes of this project. A volume is attached to at most
one server at a time, in its own location. Volumes can only be managed
while the cloud runs.

- **Create volume** opens a form for the **Size** (10 to 10,240 GB; type
  it, use the + and − buttons or drag the slider), the **Name** (proposed
  as the first free `volume-<n>`), the **Server** the volume is attached
  to, whose location it gets, the **Mount options** (**Automatic** formats
  it and mounts it at `/mnt/HC_Volume_<id>`, **Manual** formats it and
  leaves mounting to you), the **Filesystem** (ext4 or xfs) and labels.
  Without a server in the project the form cannot be sent; a volume
  without a server is created with hcloud, OpenTofu or Ansible.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- The row menu (`…`) of a detached volume offers **Attach** (to a server
  in the volume's location, optionally mounted automatically); that of an
  attached volume offers **Detach**, which detaches it from its server
  after a confirmation.
- **Delete** in the row menu deletes the volume and its data after you type
  its name. An attached volume must be detached first.
- Clicking a row opens the volume's page. **Overview** shows its size,
  location, server, status and the Linux device path
  `/dev/disk/by-id/scsi-0HC_Volume_<id>` with a copy button; **Labels**
  edits its labels; **Protection** turns the delete protection on or off.
- The actions menu (`…`) on that page offers **Rename**, **Resize** (a larger size only),
  **Attach** (to a server in the volume's location, optionally mounted
  automatically), **Detach**, **Change protection** and **Delete**.
- A volume can grow but not shrink. After a resize, grow the filesystem in
  the server with `resize2fs` or `xfs_growfs`.

Volumes is an entry of the sidebar's **Cloud** group.
