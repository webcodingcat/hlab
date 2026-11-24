# Phase 28 context

Confirmed by the user at the start of the phase (2026-11-24), unchanged from the planner's proposal.

## Assumptions

- Kind column and kind select use readable names (Server crash, Server freeze, Network, Latency, API error, Rate limit, Volume unavailable, Slow disk, Storage box unavailable); the form shows a one-line explanation per kind.
- Parameter column follows the UI-SPEC examples per kind; kinds without parameters show "–".
- Remaining time shows "4 min left" (rounded up), "45 s left" under one minute, "until ended" without duration; recalculated at the 5-second list refresh.
- Custom duration is typed as "90s", "10m", "2h" (hint "1 s to 24 h"); HLab validates it and shows the error under the field.
- Create form defaults: error code server_error, Only All, resource type All, traffic All, loss 100 %, duration Until ended; number fields empty.
- Error code select lists every known Hetzner error code, general ones first, with HTTP status ("server_error (500)"); resource type select lists All plus every top-level resource of both APIs.
- Seed field appears for API errors, and for network faults only while loss is below 100 %; accepts 0 to 9007199254740991.
- Target selects list only fitting resources (Volume unavailable: attached volumes; Slow disk: servers and volumes in two groups; latency between locations: all catalog locations); with nothing fitting the select says so and Create is disabled.
- Target column links the resource by name; a missing resource shows "Server 42".
- Active faults: Project column shows the display name linking to the project's Faults page; with the cloud stopped the usual "Start the cloud..." text.
- Top bar pill reads "1 fault active" / "N faults active", following the 2-second status poll like the tray item.
- Tray item "End all faults" ends all faults without confirmation; a failure shows an error message box; the item disappears with the next status poll.
- "End all" in a project ends that project's faults, in Active faults those of every project; both confirm without typing a name.

## Technical choices

- bridge.State gets Faults int from GET /status; TrayModel gets Faults int; new Cloud.EndAllFaults() sends DELETE /mgmt/v1/faults; tray item shown/hidden with MenuItem.SetHidden plus Menu.Update(); click/TrayTest.Click accept "End all faults".
- Fault create errors of the management API get an additive details.fields entry mapped under the fields by bridge.ts/formErrors.ts (cost of change: low).
- Error code list comes from Go (exported api.Codes()) through a bridge method; resource list from Go if a static list exists, else a fixed frontend module.
- Frontend: project view faults (list and new), area #/faults, views.json/viewInfo entries faults and active-faults with help texts, pure lib/faults.ts with unit tests, sidebar entry after Security, one shared faults table component.
- The tray calls the fault routes directly through mgmt(), not through the project proxy.
- e2e: UI-16 runs after the FAULT rows on the fault fixture's resources before releaseFaultFixture, in an order that keeps every kind creatable; UI-17 uses api_error/rate_limit faults in two small projects created through hlab fault create.

## Questions

- none
