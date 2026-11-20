# UI-SPEC: hlab

Binding rules for everything the user sees. Whoever builds user interface
follows them and invents nothing on top. What is missing here is added before
it is built.

## Foundation

- Component library or design system: own components in Svelte (no UI
  library), plain CSS with custom properties in `ui/frontend/src/theme.css`.
- Reference: the structure and workflows of the Hetzner Cloud Console
  (project list, resource sidebar, resource lists with a create button,
  resource detail pages with tabs, create forms as one page). No Hetzner
  logo, colours, fonts, icons or wording that is a Hetzner brand.
- Icons: Lucide (`@lucide/svelte`), outline style, 16 px in lists, 20 px in
  the sidebar.

## Colours

- Light and dark mode: both, following the Windows setting
(`prefers-color-scheme`); no manual switch.

| Role | Light | Dark |
|---|---|---|
| background | `#f6f7f9` | `#16181d` |
| surface (cards, tables) | `#ffffff` | `#1f2229` |
| border | `#dfe2e7` | `#2e323b` |
| text | `#1b1e24` | `#e6e8ec` |
| text muted | `#5d6472` | `#9aa1ad` |
| accent (primary buttons, links, focus) | `#2f6fdf` | `#5b8ff0` |
| success (running, healthy) | `#1f9d55` | `#3cc47a` |
| warning (starting, pending, shadowing zone) | `#c98a0a` | `#e0a53a` |
| error (failed, unhealthy, destructive) | `#d0343a` | `#ef5a5f` |

## Typography

- Typeface: `Segoe UI Variable`, fallback `Segoe UI`, `system-ui`;
  monospace `Cascadia Mono`, fallback `Consolas` (IPs, IDs, tokens, logs).
- Sizes: body 14 px / line height 20 px; small print 12 px; headings:
  page title 20 px semibold, section 16 px semibold.

## Spacing and grid

- Base unit: 4 px; steps used: 4, 8, 12, 16, 24, 32.
- Window minimum 1000 x 640 px; sidebar fixed 220 px; content max width
  1200 px, left-aligned; tables use the full content width.

## Building blocks

Every building block is built once in `ui/frontend/src/lib/` and reused
everywhere.

- Button: primary, secondary, danger, icon-only; states default, hover,
  focus (2 px accent outline), disabled, loading (spinner, label kept).
- Status pill: dot plus text, colour by role (running/success, starting/
  warning, off/muted, error).
- Resource table: header with title, count, search field (name and label
  selector), create button; sortable columns; row click opens detail; row
  menu (…) with the resource's actions; empty state with one sentence and
  the create button.
- Detail page: title with status pill and actions menu, tabs (Overview first,
  then per resource: Networking, Firewalls, Volumes, Backups, Snapshots,
  Rescue, ISO, Graphs, Services, Targets, Records, Labels, Protection).
- Form: labelled fields in one column, inline validation message under the
  field (error colour), Hetzner's field error messages from `details.fields`
  shown under the matching field; submit button at the bottom right.
- Labels field (every create form and every Labels tab): heading
  "Labels", below it the rule text "Labels are key-value pairs. Key and
  value are at most 63 characters each, start and end with an
  alphanumeric character and may contain dashes, underscores and dots in
  between. The value is optional." and one multi-line box (surface
  colour, border, about five lines high, resizable). Each label is a
  chip: the key on a muted background, the value next to it, one rounded
  border around both; a label without value shows the key only. Typing
  `key=value` or `key` and pressing Enter, Space or comma turns the text
  into a chip; a click selects a chip (key part filled with the accent
  colour, white text, accent border), Delete or Backspace removes the
  selected chip, Backspace in the empty input removes the last chip, a
  double click puts the chip back into the input for editing. A key used
  twice or a text that breaks the rule shows its message under the box
  (error colour) and stays in the input.
- Confirm dialog: for every delete and every destructive action; the
  resource name must be typed for projects (their display name), servers,
  volumes, networks and zones.
- Action toast: bottom right, shows a running action with progress and its
  result; errors stay until closed. Its title is `<Type> <name>: <action>`,
  e.g. "Network Test: Delete", "Record www A: Change TTL".
- Copy field: monospace value with a copy button (IPs, tokens, CA).
- Section tabs: a row of tabs on top of every project console page,
  below the page title, the open tab underlined in the accent colour;
  each tab has its own address, so the browser history and a reload keep
  it.
- Help button: `?` in the page title row; opens the view's help text in a
  side panel.

## Views

- Shell, built like the Hetzner Cloud Console, in three areas:
  - Top bar on every page: application icon and "HLab" (a click leads to
    the project list), breadcrumb, cloud status pill, Start/Stop button and
    a gear icon button (label "Administration") on the right. The gear is
    a toggle: a click opens Administration, a second click closes it and
    returns to the page shown before (the project list when there was
    none); while Administration is open the gear shows as pressed.
  - Project list (start page): the project cards, full width, no sidebar.
    A click on a card opens the project console of that project.
  - Project console: breadcrumb "Projects / <display name> ▾"; the name opens a
    menu to switch to another project, "Projects" leads back to the list.
    Left sidebar with the entries of this project only, in collapsible
    groups like the Hetzner Cloud Console: a group heading in uppercase
    small print (12 px, text muted) with a chevron on the right; a click
    on the heading collapses or expands the group, the collapsed groups
    are remembered; a 1 px border line separates the groups. Groups and
    entries: "Cloud" with Servers, Volumes, Firewalls; "Networking" with
    Load Balancers, Networks, IPs, DNS; after a separator line the single
    entry "Security" (no heading). Every entry opens a page with a tab
    bar on top (see "Section tabs"), also when it has only one tab:
    Servers: Servers, Snapshots, Backups, Placement Groups; Volumes:
    Volumes; Firewalls: Firewalls; Load Balancers: Load Balancers;
    Networks: Networks; IPs: Primary IPs, Floating IPs; DNS: DNS Zones;
    Security: SSH Keys, API tokens, Certificates. Snapshots and Backups
    list the project's images of that type (system images are not
    listed); ISOs are not part of the project console. The entry that
    contains the open tab is highlighted. The last opened project is
    remembered.
  - Administration (gear): breadcrumb "Administration", left sidebar with
    Server Types, ISOs, Settings, Logs.
- Cloud stopped: the project list and the project console show the text
  "Start the cloud to manage projects" with the Start button; the
  Administration (Server Types: catalog and own types, as hlabd would merge
  them; ISOs: catalog and own ISOs; Settings; Logs: last file contents)
  stays available.
- Resource list per type: resource table.
- Create per type: one form page following the Hetzner Console's order
  (servers: location, image, type, networking, SSH keys, volumes,
  firewalls, backups, placement group, labels, cloud config, name).
  Create volume follows the Hetzner Console's volume dialog: at the top a
  volume icon on the left and a large size field with + and − stepper
  buttons (1 GB steps) and the unit "GB" on the right; below it a
  full-width slider from 10 to 10240 GB, its ends labelled "10 GB" and
  "10,240 GB", bound to the same value as the field; then Name; then a
  required Server select with the hint "The volume is bound to the
  server's location." (the form has no Location field; a volume without
  a server is created through the API, hcloud or the CLI); then "Mount
  options" as a segmented control Automatic | Manual (default Automatic)
  with one line explaining the chosen option, and "Filesystem" as inline
  radios ext4 / xfs (default ext4), both always visible; then Labels. No
  price line.
- Resource detail per type: detail page with the tabs that type supports.
- Projects: the project list follows the Hetzner Console's project
  overview. Toolbar: search field (display name or name), sort menu
  (Display name, Created) with a direction arrow (default Created,
  ascending), and a grid/list switch; sort and switch are remembered like
  the last opened project. Grid: cards of equal size; list: one full-width
  row per project. A card shows a project icon and the display name large,
  the name small and muted below, a "…" menu (Delete) at the top right, and
  a footer "N resources ▾" that opens the counts per type ("2 Servers",
  "1 Volume"), each opening its section; an empty project shows
  "+ Create server" there, which opens the server create form. A last
  tile with a dashed border, "+ New project", opens the "Create project"
  dialog with the field "Display name" and, below it, the derived name
  shown live; the refusal of a taken or empty name appears under the field. Role and member
  badges of Hetzner are left out (hlab has no members). The project
  console's "API tokens"
  section has the token table and the "Create token" dialog that shows the
  token once in a copy field with a warning.
- Server Types: table of the catalog with source column (catalog, own),
  add/edit/remove own types (own rows have "Edit" and "Remove" in the row
  menu; "Edit" opens the add form filled in with the type's values, the
  name read-only; the add form chooses locations with a checkbox
  list of the catalog's locations, "<name> (<city>)", led by an "All
  locations" checkbox that is checked by default, disables the others and
  means all; without it at least one location is required), an "Update
  from Hetzner" button; it opens a dialog
  with a required password field for the Hetzner token (hint "Used once
  for the update, never stored."), "Update" and "Cancel"; the token is
  cleared when the dialog closes and never stored.
- ISOs: table of the ISO catalog and the own ISOs with source column
  (catalog, own), name, description, architecture; "Add ISO" opens a form
  with name (required, stored as the ISO's description; own ISOs have no
  Hetzner name and are shown by it everywhere), architecture (x86 or arm)
  and the source, either a URL with an optional SHA-256 or a local `.iso`
  file as an editable path with "Browse…" opening the Windows file
  dialog; own rows have "Edit" (the add form filled in) and "Remove" (confirm dialog) in the row menu.
  Available while the cloud is stopped; a local file is copied when the
  cloud reads the own ISOs.
- Settings: grouped form for every key of `settings.toml`; the trust switch
  shows the risk text from `decisions/managed-certificates.md` and a "Save
  CA certificate" button.
- Logs: monospace list, level filter, follow toggle, search field and a
  "Load older lines" button above the list. The search (case-insensitive
  text, Enter or after a short pause) runs in the service over hlabd.log
  and all rotated files and shows the matches, newest first, at most 1000,
  the matched text highlighted, with the count ("1000+ matches" when
  capped); follow is off while a search is shown, clearing the field
  returns to the live view. "Load older lines" prepends the 1000 lines
  before the oldest shown one, across rotated files, and is disabled at
  the start of the oldest file.
- Tray icon: the application icon with a status dot in the lower-right
  corner (about a third of the icon, with a 1 px contrasting rim): success
  colour while running, warning while starting, stopping or checking, text
  muted while stopped, error colour on an error or a lost connection.
- Tray menu: status line, Open HLab, one cloud item, Quit. The cloud item
  reads "Start cloud" while the cloud is stopped (also after a failed or
  refused start) and "Stop cloud" while it runs (also with the connection
  lost); while it starts, stops or its state is being checked it is
  disabled and reads "Starting cloud…", "Stopping cloud…" or "Checking
  cloud…".

## Behaviour

- Loading: skeleton rows in tables, spinner in buttons; never a blank page.
- Empty states: one sentence and the primary action.
- Errors: API errors as toast with Hetzner's code and message; form errors
  under the fields; connection loss to the service as a banner on top.
- Lists refresh every 5 seconds while visible and after every action.
- Keyboard and accessibility: every control reachable with Tab, visible
  focus, Enter submits forms, Escape closes dialogs; tables are real `<table>` elements; icons
  have labels.

## Language of the interface

- The product is called "HLab" wherever it is named as a name: window
  title, tray menu and tooltip, view texts, help texts, API and CLI
  messages, README and documentation prose. Program, command, service,
  file, folder and module names stay lowercase (`hlab.exe`, `hlabd`,
  `hlab-tray`, `hlab-dev`, `%LocalAppData%\hlab`, `hlab-ca.pem`).
- English, sentence case, short and neutral ("Create server", "Delete
  volume"); no exclamation marks; addresses the user as "you" only where
  needed.
