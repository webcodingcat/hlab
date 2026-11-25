# API tokens

The API tokens of this project. Clients such as hcloud, OpenTofu and
Ansible use a token to reach the project's API. Tokens can only be managed
while the cloud runs.

- **Create token** opens a dialog that asks for a name and whether the
  token is **Read only**. **Create** then shows the token once; copy it
  right away, it is never shown again.
- **Revoke** in a token's row menu (`…`) cuts off the clients that use it,
  after you type the token's name.
- API tokens is a tab of **Security** in the sidebar on the left, beside
  **SSH Keys** and **Certificates**.
- To switch to another project, click the project name in the breadcrumb;
  **Projects** leads back to the project list.
