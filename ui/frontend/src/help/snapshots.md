# Snapshots

The snapshots of this project's servers; the system images servers are
installed from are not listed. Snapshots can only be managed while the
cloud runs.

- A snapshot is made in a server's **Snapshots** tab; this list has no create
  form. A snapshot is not bound to its server and stays when the server is
  deleted.
- The list shows the name (the description), the size of the disk file,
  the server the snapshot was made from, labels and when it was made. The
  search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the snapshot after a
  confirmation.
- Clicking a row opens the snapshot's page. **Overview** shows its status,
  description, sizes and the server it was made from; **Labels** edits its
  labels; **Protection** protects it against deletion.
- The actions menu (`…`) on the snapshot's page offers **Edit
  description**, **Change protection** and **Delete**.

Snapshots is a tab of **Servers** in the sidebar on the left, beside
**Servers**, **Backups** and **Placement Groups**.
