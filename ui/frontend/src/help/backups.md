# Backups

The backups of this project's servers. Backups can only be managed while
the cloud runs.

- Backups are made by a server with backups enabled, in the server's
  **Backups** tab; this list has no create form. A backup is bound to its
  server and is deleted with it.
- The list shows the name (the description), the size of the disk file,
  the server the backup was made from, labels and when it was made. The
  search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the backup after you
  type its name, the description the list shows.
- Clicking a row opens the backup's page. **Overview** shows its status,
  description, sizes and the server it was made from; **Labels** edits its
  labels; **Protection** protects it against deletion.
- The actions menu (`…`) on the backup's page offers **Edit description**,
  **Change protection**, **Convert to snapshot** and **Delete**. **Convert
  to snapshot** runs at once: the backup becomes a snapshot that is no
  longer bound to its server, so it stays when the server is deleted.

Backups is a tab of **Servers** in the sidebar on the left, beside
**Servers**, **Snapshots** and **Placement Groups**.
