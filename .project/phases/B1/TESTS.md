# Phase B1 test map

Subtest names carry the requirement ID, in a group named after the
component, as in phase 04: unit tests of resource modules go through
`apitest` with `platformtest.Fake`, a fake clock and fake agent status;
rows in vendordata_test.go register through `testVendorData` in
`TestServers`. `TestHyperV` (new, internal/platform/hyperv/metrics_test.go)
tests the mapping of counter rows to VMs without PowerShell. `TestCloud`
(new, internal/cloud/recover_test.go) uses a real store with the images and
volumes modules. The e2e row runs under `TestE2E` (registered in
e2e_test.go) and shares the server and snapshot of SRV-12 through a
once-fixture instead of booting its own. Backlog item 1 (harness) has no
row: the existing rows stay green and the close notes the measurement
(PLAN.md B1-05).

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SRV-10 | a pending reset whose reset_password action is no longer running (ended `server_error` by a restart) is not in the agent document; a running one is | unit | internal/resources/servers/servers_test.go: TestServers/SRV-10/interrupted_reset | B1-01 |
| SRV-08 | rebuild gives the server seed a new instance-id `<id>-<action id>` with the new root hash and user_data; a second rebuild a different one; the rescue seed carries the current instance-id | unit | internal/resources/servers/servers_test.go: TestServers/SRV-08/instance_id | B1-01 |
| SRV-08 | rebuilding a server from a snapshot of itself with new user_data runs that user_data (per-instance modules run again) and keeps the snapshot's content | e2e | test/e2e/image_test.go: TestE2E/SRV-08/own_snapshot | B1-01 |
| SRV-01 | seed user-data without volumes, table: plain text unchanged; strict base64 decoded; base64 with a newline or invalid characters unchanged; base64(gzip(MIME)) decoded to the gzip bytes | unit | internal/resources/servers/vendordata_test.go: TestServers/SRV-01/user_data_layers | B1-02 |
| VOL-03 | seed user-data with automount volumes, table: plain, base64, gzip, MIME, base64(gzip(MIME)) and nested multipart; MIME leaf parts appear unchanged and in order before hlab's cloud-config part (which keeps merge_how and the runcmd per volume), other content as one base64 text/plain part | unit | internal/resources/servers/vendordata_test.go: TestServers/VOL-03/user_data_layers | B1-02 |
| SRV-21 | counter rows map to their VM by exact name: vCPUs, disks by path and adapters summed per VM, a VM whose name prefixes another's gets only its own rows, a VM without rows gets zeros | unit | internal/platform/hyperv/metrics_test.go: TestHyperV/SRV-21 | B1-03 |
| SRV-21 | one sample tick with several running servers makes one platform metrics call and stores each server's sample; stopped servers are not read | unit | internal/resources/servers/servers_test.go: TestServers/SRV-21/one_call | B1-03 |
| SRV-12 | images Recover removes images left `creating` with their files; available images stay | unit | internal/resources/images/images_test.go: TestImages/SRV-12/recover | B1-04 |
| VOL-01 | volumes Recover removes volumes left `creating` with their disk files; other volumes stay; the name can be used again | unit | internal/resources/volumes/volumes_test.go: TestVolumes/VOL-01/recover | B1-04 |
| VOL-01 | cloud start's recovery calls Recover of every module implementing it: a creating image and a creating volume are gone | unit | internal/cloud/recover_test.go: TestCloud/VOL-01 | B1-04 |
