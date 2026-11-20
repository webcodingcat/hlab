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

Light and dark mode: both, following the Windows setting
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

- Base unit 4 px; steps used: 4, 8, 12, 16, 24, 32.
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
- Confirm dialog: for every delete and every destructive action; the
  resource name must be typed for projects, servers, volumes, networks and
  zones.
- Action toast: bottom right, shows a running action with progress and its
  result; errors stay until closed.
- Copy field: monospace value with a copy button (IPs, tokens, CA).
- Help button: `?` in the page title row; opens the view's help text in a
  side panel.

## Views

- Shell, built like the Hetzner Cloud Console, in three areas:
  - Top bar on every page: application icon and "HLab" (a click leads to
    the project list), breadcrumb, cloud status pill, Start/Stop button and
    a gear icon button (label "Administration") on the right. The
    breadcrumb starts on every page with a home icon (accessible name
    "Projects") that leads to the project list; on the project list it is
    the current page.
  - Project list (start page): the projects table, full width, no sidebar.
    A row click opens the project console of that project.
  - Project console: breadcrumb "<home> / <name> ▾"; the name opens a
    menu to switch to another project, the home icon leads back to the
    list.
    Left sidebar with the sections of this project only: the resource
    sections (Servers, Volumes, Load Balancers, Networks, Firewalls,
    Floating IPs, Primary IPs, DNS, Certificates, SSH Keys, Placement
    Groups, Images, ISOs), then a separator and "API tokens". Phase 10
    builds "API tokens" only. The last opened project is remembered.
  - Administration (gear): breadcrumb "<home> / Administration", left sidebar with
    Server Types, Settings, Logs.
- Cloud stopped: the project list and the project console show the text
  "Start the cloud to manage projects" with the Start button; the
  Administration (Server Types: catalog and own types, as hlabd would merge
  them; Settings; Logs: last file contents) stays available.
- Resource list per type: resource table.
- Create per type: one form page following the Hetzner Console's order
  (servers: location, image, type, networking, SSH keys, volumes,
  firewalls, backups, placement group, labels, cloud config, name).
- Resource detail per type: detail page with the tabs that type supports.
- Projects: the project list is the table of projects with "Create
  project" and the row menu (delete); the project console's "API tokens"
  section has the token table and the "Create token" dialog that shows the
  token once in a copy field with a warning.
- Server Types: table of the catalog with source column (catalog, own),
  add/remove own types (the add form chooses locations with a checkbox
  list of the catalog's locations, "<name> (<city>)", led by an "All
  locations" checkbox that is checked by default, disables the others and
  means all; without it at least one location is required), an "Update
  from Hetzner" button; it opens a dialog
  with a required password field for the Hetzner token (hint "Used once
  for the update, never stored."), "Update" and "Cancel"; the token is
  cleared when the dialog closes and never stored.
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
- Keyboard: every control reachable with Tab, visible focus, Enter submits
  forms, Escape closes dialogs; tables are real `<table>` elements; icons
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
