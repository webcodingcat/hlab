## cloud-init users and vendor-data merge

Latest stable cloud-init: 26.2 (released 2026-07-29), maintained
(https://github.com/canonical/cloud-init/releases). Versions in the images (22.4, 23.4, 24.4, 25.x) were not
installed or run here; the check is by reading source and docs.

### 1. Vendor-data `users: []` stops the default user: confirmed from source (no live test)

- `ug_util.normalize_users_groups` (22.4, 23.4 and main read separately): `base_users = cfg.get("users", [])`. A list
  passes the type check silently. An empty list stays empty and `_normalize_users([])` returns `{}`. The entry `default`
  is added only if a legacy `user:` key is set (the `old_user` branch). So with `users: []` and no `user:` key no user is
  created, and `cc_users_groups` loops over nothing.
  https://github.com/canonical/cloud-init/blob/main/cloudinit/distros/ug_util.py
  (same file at tags 22.4 and 23.4).
- Only a `default` entry in `users` creates the distro default user. Docs: "users: []" is the example "Don't create any
  default user".
  https://docs.cloud-init.io/en/latest/reference/yaml_examples/user_groups.html
- Merge mechanics: `helpers.ConfigMerger._read_cfg` calls `util.mergemanydict` on this list, highest priority first:
  additional files, env file, instance configs (`cloud_config` = user-data, then `vendor2_cloud_config`, then
  `vendor_cloud_config`), datasource config, `base_cfg` (`/etc/cloud/cloud.cfg`, which holds `users: [default]` on
  stock images). `mergemanydict` default merge is `list()+dict()+str()` with dict `no_replace`: an existing key is not
  replaced and lists are not appended. The first source that has `users` wins as a whole, so vendor `users: []` beats
  the `users: [default]` in `/etc/cloud/cloud.cfg`. Docs: "User-supplied cloud-config is merged over cloud-config from
  vendor-data".
  https://github.com/canonical/cloud-init/blob/main/cloudinit/helpers.py,
  https://github.com/canonical/cloud-init/blob/main/cloudinit/util.py,
  https://github.com/canonical/cloud-init/blob/main/cloudinit/mergers/m_dict.py,
  https://docs.cloud-init.io/en/latest/explanation/vendordata.html
- Not confirmed: I read `helpers.py`, `util.py` and `m_dict.py` on main only, not at the tags 22.4, 23.4 and 24.4. I
  did not see the code for how vendor-data parts are written into `vendor_cloud_config` (CloudConfigPartHandler). The
  vendor-data merge model has been stable for years, but it is unverified per version.

### 2. user_data `users` replaces the vendor key; `users: []` adds no warning: confirmed from source

- Per the merge order above, user-data `users` (if present) wins whole over vendor `users`; there is no list append
  with the defaults. Caveat: a `merge_how` key in a cloud-config changes the merger for that config; do not set it.
- Schema: top-level `users` is `["string","array","object"]`, no `minItems`, no deprecation flag. The singular `user`
  key has no schema deprecation, but a string `user` triggers `lifecycle.deprecate` (22.2). So `users: []` (a list)
  gives no schema warning and no deprecation. Only a dict or string `users` value is deprecated (22.2), and any
  other type logs a warning.
  https://github.com/canonical/cloud-init/blob/main/cloudinit/config/schemas/schema-cloud-config-v1.json
- `cc_users_groups.handle` with empty users emits no warning. The only warning is for `ssh_redirect_user` when no
  default user exists. Do not use `ssh_redirect_user`.
  https://github.com/canonical/cloud-init/blob/main/cloudinit/config/cc_users_groups.py
- `cloud-init status` docs: "degraded" is shown when recoverable errors (WARNING level and up) occurred. The docs do not
  say whether deprecations or schema warnings count. The sources show none here, but I could not confirm it with a real
  run. https://docs.cloud-init.io/en/latest/howto/status.html

### Pitfalls

- With no default user, datasource SSH keys (`get_public_ssh_keys`) go to no account. Any user needed must come from
  user-data `users` with its own `ssh_authorized_keys`, or the VM is unreachable. The docs warn about this.
- Not checked: whether other modules (`cc_ssh` `disable_root`, `cc_set_passwords`) log a warning when no default user
  exists. A live `cloud-init status --format json` check (look at `recoverable_errors`) on one image per family
  (Debian 12, EL8, EL9, Fedora, openSUSE, Ubuntu) is the only proof of "not degraded".
## hcloud CLI image list

- Confirmed: hcloud CLI v1.70.2 `image list` always sets `hcloud.ImageListOpts{..., IncludeDeprecated: true}` (internal/cmd/image/list.go). No flag controls it. hcloud-go encodes it as `include_deprecated=true` in the query (hcloud/image.go, `Values()`; the parameter is omitted when false). `--type system` adds `type=system`. Source: https://github.com/hetznercloud/cli/blob/v1.70.2/internal/cmd/image/list.go
- Display: default columns are id, type, name, description, architecture, image_size, disk_size, created, deprecated. The `deprecated` column shows `-` when the image is not deprecated, otherwise `util.Datetime(image.DeprecationAnnounced())`, which is the announced deprecation date and time (the `deprecated` field of the API, not the removal date).
- Consequence for criterion 1: the real hcloud CLI lists deprecated images without any extra option. hlab's list leaves them out by default (images.go line ~142 only includes them with `include_deprecated=true`). To match, the hlab-side check must request `include_deprecated=true`, or the criterion must say so. A column check for fedora-43 should expect a date in `deprecated`, not `-`.
- Not confirmed: whether fedora-43 is actually deprecated in the live API today (not queried). Version v1.70.2 as the latest stable was not checked; I read the source at the v1.70.2 tag only. The source text came through a summarizing fetch, so the quoted lines are paraphrase-checked, not a byte-exact read.
