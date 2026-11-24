# Phase 29: More system images

## Interfaces

Go, additive (29-03, `internal/cidata`):

```go
type Seed struct {
	// ... existing fields ...

	// NoOnLink leaves the key `on-link` out of every route of every
	// interface in network-config. Set for images that do not render with
	// netplan (os_flavor rocky, alma, centos, fedora, opensuse): their
	// renderers ignore the key and cloud-init's v2 schema rejects it, so
	// `cloud-init status` would exit with 2 (E31, E45).
	NoOnLink bool
}
```

Servers (29-03, internal to `internal/resources/servers`): `seed(rec)` sets
`NoOnLink` from the `os_flavor` of the image object stored on the record
(`rec.Image`). The set is rocky, alma, centos, fedora and opensuse. Every
other flavor keeps `on-link`: ubuntu, debian, unknown and a record without
an image object. Snapshots carry their source's `os_flavor`, and rebuild
replaces `rec.Image`, so both follow without extra code. The rescue seed
stays as it is (always Debian 13).

Images (29-01, `internal/images`): every request of `get` (images,
checksum files, ISOs, appliance and storage debs) sends
`User-Agent: hlab/<version.Version>` (`hlab/dev` in local builds). A
non-200 answer or a transport error is retried once on the same URL. A
second 404 still wraps `errNotFound`, so the fallback pair keeps working.
`findSum` accepts, per line after trimming:
- GNU: `<hex> <file>` or `<hex> *<file>`
- BSD: `<ALGO> (<file>) = <hex>`, with ALGO SHA256 or SHA512 (the hex
  length decides the hash, as today)
It skips empty lines, `#` lines and the PGP armour of a clear-signed
file (`-----BEGIN PGP SIGNED MESSAGE-----`, the `Hash:` header, the
signature block up to `-----END PGP SIGNATURE-----`). It does not verify
the signature.

Vendor-data (29-04, `internal/resources/servers/vendordata.go`):
- The cloud-config part gains `users: []` after `ssh_pwauth`.
- The hooks call `subprocess.run(..., stdout=subprocess.PIPE,
  stderr=subprocess.PIPE, universal_newlines=True)`, with no
  `capture_output` or `text`.
- The shell part first sets `PY=/usr/bin/python3`, or
  `/usr/libexec/platform-python` when the first is missing. It writes that
  path for the placeholder `@PYTHON@` in the shebangs of `hlab-guest`,
  `hlab-volume-id` and `hlab-volume`, in the `ExecStart=` lines of
  `hlab-guest.service` and `hlab-volume@.service`, and in the udev
  `PROGRAM=`. The quoted here-documents stay; the substitution is a `sed`
  on the written files.

E2E harness (writer of `test/e2e/images_test.go`):
- `imageFixture(t, name)` creates one cx23 server in fsn1 per image the
  first time a row asks for it. It takes one slot from `serverSlots` for
  the server and holds it until both SRV-23 and SRV-24 have released that
  image, or until the group ends (cleanup in `TestE2E`, like
  `releaseNetFixture`).
- The server has the run's SSH key, private network A attached at create,
  and user_data `#cloud-config` with a `write_files` marker and a `runcmd`
  marker, without `users`.
- The image list comes from `hcloud image list --type system -o json`
  (hcloud sends `include_deprecated=true`).
- One more server (rocky-8, no SSH key, user_data with
  `users: [{name: tester, ssh_authorized_keys: [<run key>]}]` plus the
  markers) is the `extra` fixture both rows share the same way.

## Plans

- [x] 29-01 Image downloads: checksum formats, User-Agent and one retry
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CAT-09 | Tests: internal/images/images_test.go
  - Reads: ARCHITECTURE.md "images"; EXPERIMENTS.md E30; DECISIONS.md
    "EL8 hooks, checksum formats and no on-link outside netplan"; CONTEXT.md
    "Technical choices"; Interfaces above (Images)
  - Size: ~100 lines in 1 file
  - [x] `internal/images/download.go`: `findSum` reads GNU and BSD lines
    and skips `#` lines and the PGP armour of clear-signed files; the
    error for a missing entry stays `no entry for <file>`
  - [x] `internal/images/download.go`: `get` sets the User-Agent
    `hlab/<version.Version>` and retries once on a transport error or a
    non-200 answer, closing the first body; 404 after the retry still
    wraps `errNotFound`

- [x] 29-02 Catalog: the eleven new system images
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CAT-09 | Tests: internal/resources/images/images_test.go
  - Reads: `research/hetzner-system-images.json` (the eleven entries);
    EXPERIMENTS.md E30 ("Sources" and "Real API values"); SPEC.md "Stack"
    (guest images); ARCHITECTURE.md "Files and formats" (catalog files);
    CONTEXT.md "Assumptions"
  - Size: ~340 lines in 2 files
  - Adjusts: internal/resources/images/images_test.go TestImages/CAT-06:
    its "unexpected system image" and count checks contradict CAT-09. It
    keeps checking that the five CAT-06 images are listed with their
    attributes, and no longer that they are the only ones.
  - [x] `catalog/images.json`: append, in this order, rocky-8, rocky-9,
    rocky-10, alma-8, alma-9, alma-10, centos-stream-9, centos-stream-10,
    fedora-43, fedora-44 and opensuse-16 as Hetzner's objects from the
    research file, without the field `deprecated`. fedora-43 keeps its
    `deprecation` object. Each gets `_hlab.source` with `url`,
    `checksum_url` and `format: "qcow2"` from E30: the `latest` files for
    Rocky, Alma, CentOS Stream and openSUSE, the frozen versioned files for
    Fedora, and Alma's folder `CHECKSUM`. debian-11 is not added.
  - [x] Adjust TestImages/CAT-06 as above

- [x] 29-03 Seed network-config without on-link for non-netplan images
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-23 | Tests: internal/resources/servers/seed_test.go
  - Reads: ARCHITECTURE.md "cidata", "Guest integration"; EXPERIMENTS.md
    E31, E45; Interfaces above (cidata, Servers)
  - Size: ~60 lines in 2 files
  - [x] `internal/cidata/cidata.go`: `Seed.NoOnLink`; with it set, no
    route carries `on-link` (default route and private ranges)
  - [x] `internal/resources/servers/reconcile.go`: `seed(rec)` sets
    `NoOnLink` from `os_flavor` in `rec.Image` (a small helper reading the
    raw JSON; a missing or unreadable image object keeps `on-link`)

- [x] 29-04 Guest hooks for every image: Python 3.6, interpreter, root only
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-23, SRV-24 | Tests: - (proved by TestE2E/SRV-23 and SRV-24)
  - Reads: ARCHITECTURE.md "Guest integration"; EXPERIMENTS.md E32, E45;
    RESEARCH.md "cloud-init users and vendor-data merge"; DECISIONS.md
    "Servers log in as root only"; Interfaces above (Vendor-data)
  - Size: ~60 lines in 1 file
  - [x] `internal/resources/servers/vendordata.go`: Python 3.6 compatible
    `subprocess.run` arguments in `guestHook` and `volumeHook`; the
    `@PYTHON@` placeholder in the shebangs, `ExecStart=` lines and udev
    `PROGRAM=`, filled by the shell part with the interpreter it finds;
    `users: []` in the cloud-config part; doc comment of `vendorData`
    updated

- [ ] 29-05 Image matrix on the dev cloud
  - Wave: 2 | Depends on: 29-01, 29-02, 29-03, 29-04 | Review: alone
  - Requirements: SRV-23, SRV-24 | Tests: test/e2e/images_test.go
  - Reads: EXPERIMENTS.md E30-E34, E45; ARCHITECTURE.md "Guest
    integration"; CONTEXT.md "Answers"; Interfaces above (E2E harness)
  - Size: ~0-100 lines in at most 3 files (fixes only where a row fails)
  - [ ] `task build`, restart the dev service, then run
    `task e2e -- -run 'TestE2E/SRV-2[34]'`. The first run downloads and
    converts all eleven new images (about 8 GB, 1.5 to 5 minutes each).
  - [ ] Fix in `internal/images`, `internal/cidata` or
    `internal/resources/servers` what a failing case shows, within this
    phase's choices. Rerun only the failing image's case.
  - [ ] Q25: `internal/cidata` writes no `local-hostname` into `meta-data`;
    the servers module puts `hostname: <server name>` into the cloud-config
    part of vendor-data for every image (rescue included). Then rerun
    `TestE2E/SRV-2[34]` in full (the seed of every image changes).

- [ ] 29-06 Documentation
  - Wave: 3 | Depends on: 29-05 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-07 | Tests: existing tests of test/docs (no new rows)
  - Reads: SPEC.md "Documentation"; CONTEXT.md "Assumptions"; ROADMAP.md
    phase 29 "Adjusts"
  - Size: ~80 lines in 4 files
  - [ ] `README.md` (image list) and `docs/guide.md` (choosing an image:
    the sixteen images, root-only login with users from user_data,
    fedora-43 deprecated and hidden from the console's dialogs, a slow
    first create while the image downloads)
  - [ ] `docs/deviations.md` ("Servers" or "Images and backups": EL10
    images need a host CPU with x86-64-v3 (AVX2); no image creates its
    default user, Ubuntu and Debian included; debian-11 is not offered;
    fedora-43 stays usable after Hetzner's removal date until a release
    drops it) and `docs/configuration.md` (the `images.json` row names all
    images)
