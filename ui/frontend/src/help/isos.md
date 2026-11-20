# ISOs

The ISO images a server can boot from: the Ubuntu live server and Debian
netinst images of HLab's catalog and your own ISOs. The list shows while
the cloud is stopped too.

- The **Source** column says whether an ISO comes from the catalog or is
  your own. Own ISOs have no Hetzner name; they are shown by their
  **Name** everywhere, and the API lists them as `private` ISOs, which
  hcloud attaches by their ID.
- **Add ISO** opens a form for your own ISO: **Name** (required),
  **Architecture** (x86 or arm) and the **Source**, a URL or a local file.
- Source **URL**: the address HLab downloads the ISO from at its first
  attach, with an optional **SHA-256** the download is checked against.
- Source **Local file**: the **Path** of an `.iso` file on this computer;
  type it or choose it with **Browse…**. HLab copies the file when the
  cloud reads the own ISOs.
- **Edit** in an own ISO's row menu (`…`) opens the same form filled in
  with its values; **Save** applies the changes. Catalog ISOs cannot be
  edited.
- **Remove** in an own ISO's row menu (`…`) removes it after a
  confirmation. A server that has it attached keeps it until it is
  detached.
- When the file of own ISOs is broken or a local file is missing, a
  message above the table says why; `hlab cloud status` also names a
  copy that failed.
- To boot a server from an ISO, attach it in the **ISO** tab of the
  server's page.
