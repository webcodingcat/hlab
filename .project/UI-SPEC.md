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

- Shell: left sidebar with project switcher on top, then the resource
  sections (Servers, Volumes, Load Balancers, Networks, Firewalls, Floating
  IPs, Primary IPs, DNS, Certificates, SSH Keys, Placement Groups, Images,
  ISOs), then a separator and the app sections (Projects, Server Types,
  Settings, Logs). Top bar: cloud status pill and Start/Stop button.
- Cloud stopped: resource sections are disabled with the text "Start the
  cloud to manage resources"; Settings, Server Types (catalog and own
  types, as hlabd would merge them) and Logs (last file contents) stay
  available.
- Resource list per type: resource table.
- Create per type: one form page following the Hetzner Console's order
  (servers: location, image, type, networking, SSH keys, volumes,
  firewalls, backups, placement group, labels, cloud config, name).
- Resource detail per type: detail page with the tabs that type supports.
- Projects: table of projects; detail with token table, "Create token"
  dialog that shows the token once in a copy field with a warning.
- Server Types: table of the catalog with source column (catalog, own),
  add/remove own types, "Update from Hetzner" with a required password
  field for the Hetzner token, which is cleared after the update and never
  stored.
- Settings: grouped form for every key of `settings.toml`; the trust switch
  shows the risk text from `decisions/managed-certificates.md` and a "Save
  CA certificate" button.
- Logs: monospace list, level filter, follow toggle.
- Tray menu: status line, Open hlab, Start cloud / Stop cloud, Quit.

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

- English, sentence case, short and neutral ("Create server", "Delete
  volume"); no exclamation marks; addresses the user as "you" only where
  needed.
