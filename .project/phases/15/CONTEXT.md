# Phase 15 context

Confirmed at the start of the phase (2026-11-21).

## Assumptions confirmed

- Name derivation in one Go package `internal/projectname` (`Derive`, `ValidateDisplay`), used by mgmt and the tray bridge; no TypeScript copy.
- The tray's live preview of the derived name calls a new bridge method `bridge.Service.ProjectName(display) string`; it works with the cloud stopped.
- Derivation: TrimSpace, ToLower, replace only ä→ae ö→oe ü→ue ß→ss, `norm.NFD.String`, one rune loop dropping `unicode.Mn` and turning every run outside a-z0-9 into `-`, cut to 255, then trim `-` at both ends (RESEARCH option A).
- Display name: trimmed, 1-255 runes (not bytes), no control characters; inner spaces kept.
- Refusals: empty derived name → `400 invalid_input`; taken derived name → `409 conflict` naming it; no other uniqueness check.
- `POST /projects` accepts only `{"display_name":...}`; a body with only `name` is refused as `invalid_input`.
- `GET /projects` adds `display_name` and `resources_by_type` (counts above zero; order by name, the tray sorts).
- `resources_by_type` has one key per console tab: images split by type into `snapshots` and `backups`; servers, placement_groups, volumes, firewalls, load_balancers, networks, primary_ips, floating_ips, zones, ssh_keys, certificates keep their kind name. The `resources` total is unchanged.
- Store: `CreateProject(tx, name, displayName)`, `Project.DisplayName`, `CountResourcesByType(tx, projectID)`; callers in apitest and cloud tests adjusted.
- Schema version 2: migration from 1 in the existing transaction (ADD COLUMN display_name, set to name, user_version 2); no copy of the file.
- CLI `hlab project create <display-name>` takes exactly one argument and prints `project <name> created`.
- CLI `hlab project list` columns NAME, RESOURCES, CREATED, DISPLAY NAME; `--output json` passes the body through with `display_name` and `resources_by_type`.
- Tray project list: search over display name and name, sort by display name or created (default created ascending, ties by name) with direction, grid/list switch; sort and layout in localStorage; refresh every 5 s; CloudStopped while stopped.
- Card: Folder icon, display name 16 px semibold, name 12 px muted in the normal typeface, "…" menu with Delete, footer "N resources ▾" with counts per type each opening exactly one tab ("1 Snapshot", "1 Backup", ...), "+ Create server" when empty; no creation date.
- "+ New project" tile last, dashed, in grid and list; with no projects one sentence plus the tile.
- Create dialog: field "Display name", "Name: <derived>" live below; server refusals shown under the field after submit.
- Breadcrumb and switcher show display names (switcher sorted by display name); delete confirmation types the display name.
- Adjusted tests: tray e2e UI-05, `openConsole` helper, screenshot spec.
- Documentation: docs/commands.md, docs/guide.md, README, help/projects.md, README screenshots of the project list.

## Answers

- Q1 (transliteration beyond ä ö ü ß): no. Letters without a decomposition (ø æ œ ł đ ...) become `-`.
- Test map adds: PROJ-06 unit table (umlauts and ß, accents, letters without decomposition, runs of special characters, trimming, cut with `-` at the cut, rune limit); mgmt refusals 400/409 with the name; schema 1→2 migration unit test; UI-11 e2e (grid/list, sorting, a count opens its section, "+ New project" creates a project).
- The backlog UTC bug stays open (token list).
