# Volumes

The block storage volumes of this project. A volume is attached to at most
one server at a time, in its own location. Volumes can only be managed
while the cloud runs.

- **Create volume** opens a form for the **Size** (10 to 10240 GB), the
  **Name**, the **Location** and labels. Choose a **Server** to attach the
  volume at once; then the server's location applies and the form asks for
  the **Mount** option (Automatic mounts it at `/mnt/HC_Volume_<id>`,
  Manual leaves that to you) and the **Filesystem** (ext4 or xfs).
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Detach** in the row menu (`…`) detaches an attached volume from its
  server after a confirmation.
- **Delete** in the row menu deletes the volume and its data after you type
  its name. An attached volume must be detached first.
- Clicking a row opens the volume's page. **Overview** shows its size,
  location, server, status and the Linux device path
  `/dev/disk/by-id/scsi-0HC_Volume_<id>` with a copy button; **Labels**
  edits its labels; **Protection** turns the delete protection on or off.
- The actions menu (`…`) on that page offers **Resize** (a larger size only),
  **Attach** (to a server in the volume's location, optionally mounted
  automatically), **Detach**, **Change protection** and **Delete**.
- A volume can grow but not shrink. After a resize, grow the filesystem in
  the server with `resize2fs` or `xfs_growfs`.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
