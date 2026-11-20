# Images

The snapshots and backups of this project's servers; the system images
servers are installed from are not listed. Images can only be managed while
the cloud runs.

- Images are made in a server's **Backups** and **Snapshots** tabs; this
  section has no create form.
- The list shows the name (the description), the type, the size of the
  disk file, the server the image was made from, labels and when it was
  made. The search field filters by name, or with `=` by labels, for
  example `team=ops`.
- **Delete** in the row menu (`…`) deletes the image after a confirmation.
- Clicking a row opens the image's page. **Overview** shows its type,
  status, description, sizes and the server it was made from; **Labels**
  edits its labels; **Protection** protects it against deletion.
- The actions menu (`…`) on the image's page offers **Edit description**,
  **Change protection** and **Delete**. A backup also offers **Convert to
  snapshot**, which runs at once: the backup becomes a snapshot that is no
  longer bound to its server, so it stays when the server is deleted.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
