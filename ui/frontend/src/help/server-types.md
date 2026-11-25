# Server Types

The server types your projects can use: the shipped catalog and your own
types, as HLab merges them. The list shows while the cloud is stopped too.

- **Add server type** opens a form for your own type: name, description,
  cores, memory, disk and CPU type. Under **Locations**, **All locations**
  (ticked by default) offers the type everywhere; untick it and tick at
  least one location to offer it only there.
- **Edit** in an own type's row menu (`…`) opens the same form filled in
  with its values; the name stays fixed, **Save** applies the changes.
  Catalog types cannot be edited.
- **Remove** in an own type's row menu (`…`) removes it after you type its
  name. Existing servers of that type keep running; new servers cannot use it.
- **Update from Hetzner** fetches the current server types from Hetzner.
  It opens a dialog that asks for a Hetzner token; **Update** uses the
  token once and never stores it. It needs the running cloud.
