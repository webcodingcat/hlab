# Phase 29 context

Confirmed by the user at the start of the phase (2026-11-24).

## Assumptions

- The eleven new images take their attributes exactly from Hetzner's real answer in `research/hetzner-system-images.json`; the old `deprecated` field is not served.
- fedora-43 is served with Hetzner's deprecation (announced 2026-11-12, unavailable after 2027-02-09); a plain image list and the console's dialogs leave it out, API and hcloud can use it by name or ID.
- fedora-43 shows as deprecated in `hcloud image list --type system` without any change, because hcloud always sends `include_deprecated=true`.
- No date logic: fedora-43 stays usable until a later release removes it from the catalog.
- The console needs no change; new images appear under "System images" in catalog order.
- The first server from a new image downloads and converts it; nothing is downloaded ahead of time.
- Fedora downloads through download.fedoraproject.org with no fixed fallback mirror; the one retry usually hits another mirror.
- `users: []` applies to new servers and rebuilds from every image, Ubuntu and Debian included; existing servers keep their user.
- On Debian 12, `users: []` leaves a harmless schema warning in the cloud-init log; status stays done with exit 0 (E45).
- `on-link` is left out for Rocky, Alma, CentOS Stream, Fedora and openSUSE (and their snapshots), kept for Ubuntu, Debian and anything unknown; existing servers behave as before.
- SRV-23 e2e: one smallest-type server per image (16), at most three at a time from the existing server slots; volume and second private network attached while running.
- SRV-24 checks no default user on every SRV-23 server; users from user_data on one image only.

## Technical choices

- `cidata.Interface` (or `Seed`) gets a flag that turns `on-link` off; the servers module sets it from `os_flavor` of `rec.Image`; cidata knows no OS families.
- `images.findSum` learns BSD lines and skips PGP armour and `#` lines; an HTTP client wrapper sends `User-Agent: hlab/<version>` and retries once on non-200, for images and checksums.
- `vendordata.go` hooks use `stdout=PIPE, stderr=PIPE, universal_newlines=True`; the shell script finds the interpreter (`/usr/bin/python3`, else `/usr/libexec/platform-python`) and writes it into shebangs, `ExecStart=` and udev `PROGRAM=`; `users: []` goes into the cloud-config part.
- `catalog/images.json` entries are the API objects plus `_hlab.source` (E30 URLs); Rocky, Alma, CentOS Stream, openSUSE use `latest` files, Fedora its frozen versioned files.
- E2E in a new `test/e2e/images_test.go` with a shared per-image fixture; SRV-23 and SRV-24 one row each with one case per image; CAT-09 extends the CAT-06 table in `internal/resources/images/images_test.go`.
- The criterion-1 check runs through the hcloud CLI and expects a date in fedora-43's deprecated column.

## Answers

- Q1 (SSH key vs. create-time password in SRV-23): one server per image with an SSH key; the password is proven through the password reset followed by a real SSH login with that password on every image (so sshd allows password login on each image); plus one keyless server on a single image for the create-time password.
