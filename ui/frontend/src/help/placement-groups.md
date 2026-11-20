# Placement Groups

The placement groups of this project. A spread placement group would run
each of its servers on its own host; HLab runs every server on your one
machine, so a group only records its servers. Placement groups can only be
managed while the cloud runs.

- **Create placement group** opens a form with the **Name**, the **Type**
  (always `spread`, the only type) and labels.
- A spread group holds at most 10 servers. Servers join a group when they
  are created or with **Add to placement group** in a server's actions menu.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the group after a confirmation,
  also while it still has servers; they then belong to no group.
- Clicking a row opens the group's page. **Overview** shows the type and
  its servers, each linking to the server's page; **Labels** edits its
  labels.
- The actions menu (`…`) on the group's page offers **Rename** and
  **Delete**.

Placement Groups is a tab of **Servers** in the sidebar on the left, beside
**Servers**, **Snapshots** and **Backups**.
