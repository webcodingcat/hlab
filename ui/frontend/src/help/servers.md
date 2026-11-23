# Servers

The servers of this project: real Hyper-V VMs. Servers can only be managed
while the cloud runs. The list and an open server refresh every 5 seconds.

- **Create server** opens the create form.
- The search field filters by name, or with `=` by labels, for example
  `env=dev`.
- Click a server to open its detail page.
- The row menu (`…`) holds **Power on**, **Shutdown**, **Reboot**,
  **Reset**, **Power off**, **Console** and **Delete**.

## Create a server

Choose **Location**, **Image** and **Type**; the type list shows only the
types available in that location. Below them: public IPv4 and IPv6 (a new
or a free primary IP), private networks, **SSH keys**, free **Volumes** of
the location, **Firewalls**, **Enable backups**, **Placement group**,
labels, **Cloud config** and the **Name**, which HLab proposes as
`<type>-<location>-<n>`. **Create server** sends the form; a toast follows
the creation and the detail page opens.

Without SSH keys the server gets a root password. It shows once in a
dialog with a copy button; copy it before you close the dialog.

## The detail page

The `…` button next to the name opens the actions menu:

- **Power on**, **Shutdown** and **Reboot** run at once. **Reset** and
  **Power off** cut the power and ask first.
- **Reset root password** shows a new root password once.
- **Rebuild** replaces the disk with a chosen image and asks first.
- **Change type** needs a stopped server. With **Upgrade disk** the disk
  grows to the new type's size, which cannot be undone.
- **Rename**, **Add to placement group** and
  **Remove from placement group**.
- **Console** opens the server's screen in a window of its own, see
  below.
- **Delete** deletes the server and its disk; type its name to confirm.

The tabs:

- **Overview**: status, type, location, image, addresses and placement
  group.
- **Networking**: reverse DNS of the public IPs (**Edit**,
  **Reset to default**, **Add IPv6 reverse DNS**); **Attach to network**,
  **Change alias IPs** and **Detach** for private networks.
- **Firewalls**: **Apply firewall**, and **Remove** for a firewall applied
  to this server.
- **Volumes**: **Attach volume** and **Detach**.
- **Backups**: **Enable backups** (one backup a day, the 7 newest kept),
  **Create backup**, **Disable backups**, which deletes the server's
  backups.
- **Snapshots**: **Create snapshot**. In both lists, **Restore** writes the
  image back to the server's disk and **Delete** deletes it, each after a
  confirmation.
- **Rescue**: **Enable rescue** with the SSH keys for the rescue system;
  the next power on, reboot or reset boots it. **Disable rescue** ends it.
- **ISO**: **Attach ISO** and **Detach ISO**. The first attach of an ISO
  downloads it.
- **Graphs**: CPU, disk and network for 1 hour, 24 hours or 7 days. HLab
  keeps 24 hours of samples while the cloud runs.
- **Labels**: type `key=value` or `key` and press Enter, Space or a
  comma to add a label as a chip; click a chip and press Delete to remove
  it, or double-click it to edit it. Then **Save labels**. The labels of
  the create form work the same way.
- **Protection**: protection against deletion and against rebuild,
  then **Save**.

## The console window

The window "Console: <server name>" shows the server's screen, scaled to
the window; click into it to type and use the mouse. It works whether or
not the HLab CA is trusted.

- The pill shows **Connecting**, **Connected** or **Disconnected**, with
  the reason after a drop.
- **Send Ctrl+Alt+Del** sends that key combination to the server.
- **Reconnect** appears after the connection drops and connects again.
- While the server is off, starting or stopping, the screen shows its
  last picture or black until there is a live picture again.
- **Console** on a server whose window is open brings that window to the
  front. Closing the window ends the connection.
- An open console costs about 0.7 CPU cores, almost all in the Hyper-V
  service `vmms.exe`.

Servers is an entry of the sidebar's **Cloud** group; its tabs also lead
to **Snapshots**, **Backups** and **Placement Groups**.
