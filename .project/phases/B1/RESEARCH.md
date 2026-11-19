# B1 Research

Checked 2026-11-19 against the cloud-init `main` branch source and tags, Debian tracker, Launchpad and the provider repositories. Items I could not confirm are marked "unclear".

## Versions of cloud-init in the target images

- Ubuntu 22.04: 26.1-0ubuntu1~22.04.1 (updates, 2026-06-01); 25.1.4 security; release 22.1.
- Ubuntu 24.04: 26.2-0ubuntu1~24.04.2 (updates, 2026-10-23); release 24.1.3.
- Ubuntu 26.04: 26.2-0ubuntu1~26.04.2 (updates); release 26.1.
- Debian 12: 22.4.2-1+deb12u4. Debian 13: 25.1.4-1+deb13u1.
- Sources: https://launchpad.net/ubuntu/+source/cloud-init and https://tracker.debian.org/pkg/cloud-init
- cloud-init is maintained (26.2 published October 2026).
- A cached image may carry an older package than the archive. The range to cover is 22.1 to 26.2. The behaviour below is the same across it, except where noted.

## Q1. Hetzner datasource, gzip, base64, OpenTofu, hcloud provider

- Hetzner datasource decodes base64 itself: confirmed.
  - `DataSourceHetzner._get_data` sets `self.userdata_raw = util.maybe_b64decode(ud)` on `main`. At tag 22.4.2 it is `hc_helper.maybe_b64decode(ud)`, the same behaviour.
  - Comment in the source: "Hetzner cloud does not support binary user-data. So here, do a base64 decode of the data if we can. ... a user can provide base64 encoded (possibly gzipped) data".
  - `maybe_b64decode` calls `base64.b64decode(data, validate=True)` and returns the input unchanged on `binascii.Error`. So plain text that is not valid base64 passes through. Whitespace or newlines inside the base64 make `validate=True` fail, and the data is then NOT decoded.
  - This is a Hetzner datasource feature only. NoCloud and the core do not decode base64 at the top level. If hlab serves user_data through NoCloud, hlab must decode base64 itself to match Hetzner.
  - The source also notes that `/var/lib/cloud/instance/cloud-config.txt` holds the decoded data.
  - Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/sources/DataSourceHetzner.py and https://github.com/canonical/cloud-init/blob/22.4.2/cloudinit/sources/DataSourceHetzner.py
- Gzip at the top level: confirmed, done by the core, not by the datasource.
  - `user_data.convert_string` calls `util.decomp_gzip(bdata, decode=False)` on the whole raw document before it looks for MIME. The result is then parsed as MIME if `mime-version:` (case-insensitive) is in its first 4096 bytes, and is otherwise wrapped as a single part.
  - The order is therefore: Hetzner base64 decode, then the core gunzip, then MIME or plain detection. This is the only layered chain cloud-init supports at the top.
  - A MIME document needs a `MIME-Version:` header within the first 4096 bytes, or it is treated as one plain part.
  - Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/user_data.py
- Inside a MIME part:
  - `_process_msg` walks the parts and gets each body with `util.fully_decoded_payload(part)`. That honours `Content-Transfer-Encoding` (base64, quoted-printable) through `get_payload(decode=True)`. For text parts with 7bit, 8bit, binary or no encoding the body is used as is.
  - If the part's `Content-Type` is in DECOMP_TYPES, the decoded payload is gunzipped. DECOMP_TYPES: application/gzip, application/gzip-compressed, application/gzipped, application/x-compress, application/x-compressed, application/x-gunzip, application/x-gzip, application/x-gzip-compressed. After that the type is re-sniffed from the content.
  - A part of type `text/plain` is NOT gunzipped. Its payload is only sniffed for `#cloud-config`, `#!` and similar starts. A MIME document or gzip inside a `text/plain` part is not unpacked and is silently ignored. This explains the bug in `seedUserData`.
  - Base64 inside a part works only through the part's own `Content-Transfer-Encoding: base64` header (the current wrapper sets it correctly). A base64 body without that header is not decoded.
  - Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/user_data.py and `fully_decoded_payload` in https://github.com/canonical/cloud-init/blob/main/cloudinit/util.py
- OpenTofu/Terraform `cloudinit_config` (hashicorp/cloudinit provider):
  - Default is `gzip = true` and `base64_encode = true`. `base64_encode` cannot be false when gzip is true.
  - The MIME document is written first, then gzip is applied to the whole document, then base64 (`base64.StdEncoding`, one line, no wrapping). So the output is base64(gzip(MIME)).
  - The top-level headers are `Content-Type: multipart/mixed; boundary="MIMEBOUNDARY"` and `MIME-Version: 1.0`.
  - Per part: `Content-Type` (default text/plain), `MIME-Version: 1.0`, `Content-Transfer-Encoding: 7bit`, `Content-Disposition: attachment; filename="..."` if set, and `X-Merge-Type` if `merge_type` is set. The header is `X-Merge-Type`, not `Merge-Type`. cloud-init accepts both.
  - With `gzip = false, base64_encode = false` it is plain MIME. With `gzip = false, base64_encode = true` it is base64(MIME).
  - Source: https://github.com/hashicorp/terraform-provider-cloudinit/blob/main/internal/provider/cloudinit_config.go and `data_source_cloudinit_config.go`
- hcloud provider and user_data:
  - `resourceServerCreate` assigns `d.Get("user_data").(string)` to `hcloud.ServerCreateOpts.UserData` with no encoding of its own. The string goes to the API as given, so it is the base64 text from `cloudinit_config`. The API itself does not decode it; the guest's Hetzner datasource does.
  - `user_data` has `ForceNew`. State holds a SHA-1 hash, and a diff is suppressed when the hash matches.
  - Source: https://github.com/hetznercloud/terraform-provider-hcloud/blob/main/internal/server/resource.go
  - Not checked: whether hcloud-go base64-encodes the field on the wire (as far as I know it does not). The Hetzner API's size limit for user_data (32 KiB, from memory) is also not confirmed.
- Consequence for `seedUserData`: layers base64, then gzip, then MIME, as planned. With layers decoded by hlab, the user's MIME can be nested directly. Whether the user document is decoded fully or only to the point where it is a valid MIME document is for the plan to decide. Both work with cloud-init.

## Q2. NoCloud: changed instance-id on a restored disk

- Confirmed: a new instance-id on a disk that carries state of an older instance makes cloud-init treat the boot as a new instance, and all per-instance modules run again.
  - Default `manual_cache_clean: false` means "check" mode: the cached instance id is compared with the one found at runtime. If they differ, "this is a new instance's first boot. The cache is cleaned and per-instance configuration runs." Source: https://docs.cloud-init.io/en/latest/explanation/first_boot.html
  - `Init._reflect_cur_instance` makes a new `/var/lib/cloud/instances/<iid>/` directory, so `sem/` is empty and per-instance semaphores (set_passwords/chpasswd, ssh, scripts_user, runcmd, scripts_vendor, users_groups) are new. `is_new_instance()` is true when `data/instance-id` differs. Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/stages.py
  - NoCloud documentation: "if you are making updates to user-data you will also have to change the instance-id" (or start from a fresh disk). Source: https://docs.cloud-init.io/en/latest/reference/datasources/nocloud.html
- Caveats:
  - `DataSourceNoCloud.check_instance_id` reads only the kernel command line and seed directories (`meta-data` in `seed_dirs`), not a `cidata` device. For a `cidata` ISO/FAT disk it returns None, so the cache is not trusted in "check" mode and the datasource is found again. That gives the new id. If hlab delivers NoCloud through a seed directory or the kernel command line, `check_instance_id` compares the quick-read id and also catches the change. Either path works.
  - `manual_cache_clean: true` ("trust" mode) never detects a new instance. Only a manual `cloud-init clean` helps. The stock Ubuntu and Debian images do not set it. Switching a trust-mode image back to check is not supported. Not checked here: whether the images hlab uses set it. Check in the e2e row.
  - A new instance also reruns everything else per-instance. Visible effects: SSH host keys are regenerated (`ssh` module; clients see a changed host key), network config is regenerated from the datasource, and `write_files` and `runcmd` run again. Modules with frequency "once" (a few, for example `users_groups` default user creation is per-instance) may behave differently. The list was not walked module by module.
  - Frequency `always` and per-boot modules are unaffected.
  - Files in `/var/lib/cloud/instances/<old-iid>` stay on disk, and `previous-instance-id` is recorded. That is harmless.
  - A server whose hlab record keeps instance-id = server ID after a rebuild from its own snapshot (the bug) matches the cached id and runs only per-boot modules, as the plan states.
  - Whether the e2e row will find the password working depends on cloud-init finishing; the standard readiness wait of the harness applies.

## Q3. MIME parts copied into an outer multipart

- Confirmed from the source; not tested on a running guest.
  - Nested `multipart/*` containers are skipped (`is_skippable`), and `_process_msg` uses `base_msg.walk()`. So all leaf parts of a user document inside an outer multipart are handled as if they were top-level parts, in document order. A nested `multipart/mixed` part therefore works, provided it is a real `multipart/*` part with its own boundary. Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/user_data.py
  - Part headers are not rewritten on attach. `_attach_part` only adds a default filename `part-NNN` (if none) and the launch index. `Content-Type` is rewritten only when the sniffed type differs for `text/plain` and `text/x-shellscript`. So `Merge-Type` and `X-Merge-Type` headers stay on the part.
  - `Merge-Type` is read by the cloud-config handler, per part: it checks `Merge-Type`, then `X-Merge-Type`, and also reads `merge_how` from the YAML (content). The header and content mergers are joined. A part with neither falls back to `dict(replace)+list()+str()`. A `#cloud-config-jsonp` part ignores them. Source: https://github.com/canonical/cloud-init/blob/main/cloudinit/handlers/cloud_config.py
  - The merge is applied per part in order onto one buffer, so the order of the parts decides the result. hlab's own `merge_how: "list(append)+dict(no_replace,recurse_list)+str()"` is a content key and affects only the merge of its own part into the buffer built from earlier parts (the user's parts). It must come AFTER the user's parts, as in the current code.
  - `text/x-shellscript` parts run as scripts (`scripts-user`), saved under the part's filename (or `part-NNN`). Two parts with the same `Content-Disposition` filename overwrite each other. A user part named like an hlab part would collide.
  - Gzip parts (`application/gzip`, `application/x-gzip`) work inside the outer document, if their `Content-Transfer-Encoding` is base64 (binary bytes are otherwise not safe in a text MIME document).
  - Python's email parser reads the part. It is strict on the boundary line: the boundary of the outer document must not occur in the user's content.
  - A user document that has no `MIME-Version` header within 4096 bytes is not MIME to cloud-init. This applies to a part only if the part is itself a nested document in a `text/plain` part, which is the bug.
- Unclear: behaviour of a `text/x-include-url` or `text/cloud-config-archive` part inside the outer document. The source handles both through `_process_msg` recursively, so it should work, but I did not trace it.

## Q4. Hyper-V performance counters from PowerShell

- Instance names (the current `metricsScript` assumes these):
  - Virtual processor class: `<VM name>:Hv VP <n>`; the root partition uses `Root VP <n>`; `_Total` instances exist. Confirmed by secondary sources only (Microsoft TechNet blog archive and Veeam/OpsRamp documents), not by a Microsoft reference page: https://learn.microsoft.com/en-us/archive/blogs/tvoellm/hyper-v-performance-counters-part-four-of-many-hyper-v-hypervisor-virtual-processor-and-hyper-v-hypervisor-root-virtual-processor-counter-set and https://helpcenter.veeam.com/docs/mp/hyperv_reference/vm.html
  - Virtual network adapter class: `<VM name>_<adapter name>_<id>`. Only indirect evidence (a script that splits the instance name on `_`). Unclear, not a documented format.
  - Virtual storage device class: the VHD path, with the code in hlab turning `\` into `-`. Not confirmed by documentation here; the code was written and tested in phase 03.
  - Pitfall: matching by prefix is ambiguous when one VM name is the prefix of another (`a_` vs `a_b_`), for the network adapter: VM `a` and VM `a_b` both match `a_`. With `MetricsAll` it is better to map the whole name from the instance by splitting at the known suffix, or by matching exact VM names from the list from longest to shortest. Hlab server VM names are likely IDs without `_` or `:`, but I did not check how they are formed.
- Cost per query:
  - No authoritative figure found. The `Win32_PerfRawData_*` classes are raw counters. They need no sampling interval, unlike `Win32_PerfFormattedData_*`. A `Get-CimInstance` on such a class returns the rows for ALL instances (all VMs) in one call; the cost then grows with the number of instances (vCPUs, disks and NICs), not with the number of calls. One query per class for all VMs is the cheap shape, and filtering by name in PowerShell is cheap compared with the query. The current script repeats the three full queries per VM, so it costs N times as much as needed. That follows from how the classes work, and I did not find a measurement for it.
  - Fixed cost per PowerShell call: the process is already running (single mutex-serialized process), so there is no startup cost, but `Get-VM`/`Get-VMHardDiskDrive` per VM in the script add more calls. With `MetricsAll`, `Get-VMHardDiskDrive` is also needed only once (`Get-VM | Get-VMHardDiskDrive`) or can be dropped if the disk instance name can be matched in another way.
- Measurement on this machine: not done. My tool set in this run had no shell, so I could not run PowerShell. I cannot say whether the counters are readable without elevation (Hyper-V counters usually need membership of Hyper-V Administrators or Performance Monitor Users). The e2e or a manual run on the host can show the number.
- Raw counter semantics for the CPU delta (100 ns `PercentTotalRunTime` over `Timestamp_Sys100NS` deltas) stay as in the existing `cpuPercent`. With several VPs per VM the sums of both are over the VPs of one VM, and `Timestamp_Sys100NS` is the same for the rows of one query.

## Summary

- Q1 confirmed (versions across 22.1 to 26.2). Unclear: hcloud-go wire encoding and the 32 KiB limit.
- Q2 confirmed from docs and source; not tested on a guest. Unclear: whether the hlab images set `manual_cache_clean`.
- Q3 confirmed from source; not tested on a guest. Unclear: include-url and archive parts inside the outer document.
- Q4 partly: instance names confirmed by secondary sources only (the NIC format is an inference); cost per query and the unelevated measurement not done (no shell available).
