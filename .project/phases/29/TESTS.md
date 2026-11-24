# Phase 29 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| CAT-09 | with the shipped catalog, `GET /images?type=system&include_deprecated=true` (as hcloud sends it) lists rocky-8, rocky-9, rocky-10, alma-8, alma-9, alma-10, centos-stream-9, centos-stream-10, fedora-43, fedora-44 and opensuse-16 with Hetzner's ID, description, os_flavor, os_version, disk_size 5, rapid_deploy true, created, type system, status available and architecture x86 (table, one case per image, values from `research/hetzner-system-images.json`), and no field `deprecated`. fedora-43 has `deprecation` {announced 2026-11-12T00:00:00Z, unavailable_after 2027-02-09T00:00:00Z} and the others `null`. The plain `GET /images?type=system` lists the ten others but not fedora-43. debian-11 is listed in neither | unit | internal/resources/images/images_test.go: TestImages/CAT-09 | 29-02 |
| CAT-09 | an image is downloaded and verified when its checksum file is GNU (`hash  name`), BSD (`SHA256 (name) = hash` with `#` comment lines) or PGP clear-signed BSD with several entries (table, one case each); a BSD entry with a wrong digest fails with no cached base and no conversion | unit | internal/images/images_test.go: TestImages/CAT-09_checksums | 29-01 |
| CAT-09 | from a source that answers 403 to any User-Agent not starting with `hlab/`, the image and its checksum file download and verify. A source that answers 503 once and then 200 succeeds on the retry. A source that answers 503 every time fails, with no cached base | unit | internal/images/images_test.go: TestImages/CAT-09_mirror | 29-01 |
| SRV-23 | the seed's network-config of a created server carries `on-link` on the default route and on the private network's range route for ubuntu-24.04 and debian-13, and none on any route for rocky-9, alma-10, centos-stream-10, fedora-44 and opensuse-16 (table, one case per flavor). A server created from a snapshot of a rocky-9 server has none; a rocky-9 server rebuilt to debian-13 has it again | unit | internal/resources/servers/seed_test.go: TestServers/SRV-23 | 29-03 |
| SRV-23 | `hcloud image list --type system` shows all sixteen images of CAT-06 and CAT-09, with a date in fedora-43's deprecated column. For each image (table, one case per image) a cx23 server with the run's SSH key, private network A at create and a user_data marker: has its name as hostname; takes a root SSH login with the key; has the user_data marker; reaches the internet; has its IP of network A; `cloud-init status --wait` exits 0. A 10 GB volume with automount attached while running is mounted at `/mnt/HC_Volume_<id>`. Network B attached while running gives its IP. After `reset_password`, an SSH login as root with the new password succeeds. Case `extra` (rocky-8, created without SSH key): an SSH login as root with the create-time password succeeds | e2e | test/e2e/images_test.go: TestE2E/SRV-23 | 29-05 |
| SRV-24 | on each image's SRV-23 server (table, one case per image) `getent passwd` has no account with UID 1000 to 59999 and none named ubuntu, debian, rocky, almalinux, cloud-user, fedora or opensuse. On the `extra` server, whose user_data sets `users` with `tester` and the run's key, `tester` exists and logs in with that key, while no default user exists | e2e | test/e2e/images_test.go: TestE2E/SRV-24 | 29-05 |

Not mapped:
- The vendor-data text (`users: []`, the Python 3.6 arguments, the
  interpreter placeholder) is configuration. Booting rocky-8 and alma-8 in
  SRV-23 (password reset, volume, hot-added network) and the default-user
  checks in SRV-24 prove it on the real images.
- The catalog's `_hlab.source` entries are configuration. SRV-23 downloads
  each image through them.
- DOC-01 to DOC-04 keep their existing tests in test/docs; DOC-07 is
  manual.

## Writers

1. Go unit, ~320 lines:
   - internal/images/images_test.go: CAT-09_checksums and CAT-09_mirror.
     They extend the existing test source (`newSource`) with a checksum
     format, a User-Agent check and an answer sequence.
   - internal/resources/images/images_test.go: CAT-09.
   - internal/resources/servers/seed_test.go (new): `testSeed`, called
     from `TestServers` in servers_test.go next to `testVendorData`. It
     reuses `newFakeEnv`, `createdServer` and `seedEthernet` from
     adapters_test.go.
2. e2e, ~450 lines:
   - test/e2e/images_test.go (new): `imageFixture` and the `extra`
     fixture as in PLAN.md "Interfaces", `testSRV23` and `testSRV24`, each
     with one parallel subtest per image.
   - test/e2e/e2e_test.go: both rows registered with `parallel(...)`, plus
     the fixture's release in the group's cleanup.
   - Each subtest waits for boot, SSH and `cloud-init status --wait` with
     deadlines of its own. The first run's downloads count against the
     rows' time limit.
