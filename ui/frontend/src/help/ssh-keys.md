# SSH Keys

The SSH keys of this project. New servers and the rescue system get the
keys chosen from this list. SSH keys can only be managed while the cloud
runs.

- **Add SSH key** opens a form for the **Public key** in OpenSSH format
  (for example the content of `id_ed25519.pub`), the **Name** and labels.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- Click a key to open it. **Overview** shows the fingerprint and the public
  key with a copy button; change the name there with **Save name**.
  **Labels** edits the labels, saved with **Save labels**.
- **Delete** in the row menu (`…`) or in the `…` menu of an open key
  deletes the key after a confirmation. Servers that already use it keep
  it.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
