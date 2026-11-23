## SMB from the Windows host: system error 67 (SBOX-22)

Checked on 2026-11-23, Windows 10.0.26300, dev service `hlab-dev` running,
cloud `running`, without elevation. Throwaway project `r22-smb` with boxes
`u100082` (198.19.4.20) and `u100083` (198.19.4.21), bx11,
`reachable_externally`, `samba_enabled` and `ssh_enabled` on, and one
subaccount `u100083-sub1` (Samba on). The project was deleted afterwards,
which removed the boxes and the subaccount.

### What was tried

Driven like the e2e helpers: `hlab project create`, `hlab token create
--output json`, then `POST /hetzner/v1/storage_boxes`,
`.../actions/reset_password`, `.../subaccounts`, waiting through
`GET /v1/actions?id=`. Each `net use` call was made from a PowerShell script
file, with the password passed as an argument and never printed.

1. One box: `net use \\u100082.storagebox.hetzner.internal\backup`,
   `\\198.19.4.20\backup` and `\\198.19.4.20\IPC$`, all with
   `/user:u100082 /persistent:no`.
2. Two boxes: the same three forms against each box.
3. A subaccount share: `net use
   \\u100083-sub1.storagebox.hetzner.internal\u100083-sub1`.
4. The same paths passed through Git Bash: the Bash tool, and `bash -c`
   started from PowerShell, with the path unquoted, single-quoted and
   double-quoted.
5. Controls: `net use \no-such-host.invalid\backup` (one leading
   backslash), `\\no-such-host.invalid\backup` and `\\192.0.2.1\backup`
   (unreachable).

### What was observed

- Steps 1 to 3 all worked with a single box and with two boxes. Writing
  `r22.txt` and reading it back worked on `backup` of both boxes, through
  the name and through the IP. `IPC$` connected. The subaccount share was
  written and read back. `Get-SmbMapping` showed each mapping as `OK`. A
  mapped drive with read and write requires negotiate, session setup
  (NTLMv2) and tree connect to succeed, so none of them is the problem.
- Step 4 failed in every form with `System error 67` ("Der Netzwerkname
  wurde nicht gefunden"). This covers the box name, the IP and `IPC$`,
  which is exactly the phase 20 pattern. The argument that reached
  `net.exe` was `\198.19.4.20\backup`. When unquoted, it was even
  `198.19.4.20backup`. Git Bash (and the Bash tool before it) collapses the
  leading `\\`. The forward-slash form `//198.19.4.20/backup` is read by
  `net` as an unknown option.
- Step 5: a path with one leading backslash fails with 67 in 0.1 s, with no
  network traffic. An unknown name also returns 67 in 0.1 s. An
  unreachable IP returns 67 only after 42 s. Error 67 is the redirector
  layer's generic "no provider claimed the path". It says nothing about
  SMB itself.
- The WebDAV redirector is not involved. The `WebClient` service is
  `Stopped` (start type Manual) before and after the failures.
- `Get-SmbClientConfiguration`: RequireSecuritySignature True,
  EnableSecuritySignature True, EncryptData empty, RequireEncryption False,
  EnableMultiChannel True, BlockNTLM False, EnableInsecureGuestLogons
  False, Smb2DialectMin 0 and Max 65535. Signing is required by the client,
  and Samba provides it (E16: AES-128-GMAC). NTLM is not blocked.
- These need elevation and cannot be read: `Get-SmbConnection` returns
  "Zugriff verweigert". The event logs `Microsoft-Windows-SMBClient/
  Connectivity`, `/Operational` and `/Security` cannot be read either ("nicht
  autorisierter Vorgang").
- Network path: `Find-NetRoute 198.19.4.20` leaves from
  `vEthernet (hlab-dev)` with source address 198.19.0.2, through the route
  198.19.0.0/16 via the appliance at 198.19.0.1. WinNAT `hlab-dev` covers
  198.19.0.0/16. TCP 445 is open. SMB works over this routed path with this
  source address, so neither the appliance path, the source address
  198.19.0.2 nor the WinNAT prefix makes a difference compared with E16
  (Default Switch).

### Root cause

This is not a fault in hlab, Samba or the Windows SMB client. The `net use`
calls of phase 20 went through Git Bash, which turns `\\host\share` into
`\host\share` or `hostshare`. `net.exe` rejects that path with error 67
before it sends any SMB packet. E16 already notes "Git Bash eats `\\`".
The current Samba config needs no change. Phase 20 could not be replayed
literally (its commands are not recorded), so the cause is inferred from a
reproduction of the identical symptom. It was not observed directly.

### Confirmed fix

Run `net use` with the UNC path intact: from PowerShell or cmd, a script
file, or Go `exec.Command("net", "use", `\\`+server+`\`+share, pw,
"/user:"+user, "/persistent:no")` (no shell in between). Confirmed in this
run: mapping, writing and reading back worked for `backup` of two boxes
(through the name and the IP), for `IPC$`, and for a subaccount share.
For the SBOX-22 e2e test: call `net.exe` directly and never through a
shell, check that the exit code is 0, and delete the mapping with
`net use <path> /delete /y`. `Get-SmbMapping` works without elevation and
can show the mapping; `Get-SmbConnection` needs elevation.
## Samba 4.22 server behaviour

Installed: Samba 4.22.11 (Debian 13, E16/E17 in EXPERIMENTS.md). I did not check the current stable release, so I can't say whether 4.22.x is still the newest. Samba is maintained. Sources are the 4.22 docs-xml and source files on GitHub (`v4-22-stable`). The rendered man page returned 503.

Important context: E16 (EXPERIMENTS.md, 2026-11-21) already ran Windows `net use` successfully against the same config shape (standalone, bind interfaces only, restrict anonymous = 2, `include = ip-%i.conf`, two box IPs, Windows 10.0.26300). The failure is therefore not caused by the config shape alone. Differences from E16 to look at are the number of IPs, the real file content, `disable netbios`, `durable handles` and the real network path.

**server multi channel support**
- Default is yes on Linux and FreeBSD since 4.15. It is experimental only up to 4.14. [docs-xml/smbdotconf/protocol/servermultichannelsupport.xml]
- FSCTL_QUERY_NETWORK_INTERFACE_INFO (source3/smbd/smb2_ioctl_network_fs.c) calls `load_interfaces()`, then walks `iface_count()`. It skips non-IP entries and entries with linkspeed 0, and copies ifindex, capability and linkspeed. Without clustering there is no per-connection filtering. The excerpt does not show how `load_interfaces()` honours `interfaces =`, so I could not confirm that. It is normally limited to the configured `interfaces` list. Expect the reply to list all box IPs, not only the one the client connected to.
- If multichannel is not enabled for the client, the ioctl returns INVALID_DEVICE_REQUEST (IPC$: FS_DRIVER_REQUIRED).
- Windows can therefore open extra TCP connections to other box IPs. I could not confirm in source that a channel connection is handed to the session-owning smbd. The `smb2_server.c` page was truncated and I only read the first 100000 of 137252 characters. My recollection is that Samba passes it to the owner process, which keeps the first connection's config, so tree connects would not hit the other IP's include. Unconfirmed.
- Windows default is multichannel on, but it only uses it when the interface pairs qualify. `Get-SmbMultichannelConnection` on the host shows what it did. `Set-SmbClientConfiguration -EnableMultichannel $false` on the host, or `server multi channel support = no` on the server, rules it out cheaply.
- The first tree connect (IPC$) happens on the first connection, so multichannel does not plausibly explain BAD_NETWORK_NAME on IPC$. Treat it as a low-probability cause.

**`%i` and `include = ip-%i.conf`**
- `include` supports the standard substitutions except %u, %P and %S. [include.xml] `%i` is the server IP the client connected to.
- Samba forks one smbd per TCP connection. The include is expanded when that process loads or reloads its config. The parent listener has no client address, so it reads `ip-0.0.0.0.conf` or nothing (hence the empty file, E16). The evaluation is per TCP connection, not per tree connect. I did not find a source line showing exactly when `%i` is set relative to the config load. Unconfirmed in code, but E16 shows it works with Windows.
- IPC$ is built in and does not come from the include. It is the same on every IP. A missing share in an include therefore cannot explain BAD_NETWORK_NAME for IPC$ unless the connection ended up in a different process, as in the multichannel case above. I could not fetch `service.c` (404), so the exact condition for BAD_NETWORK_NAME on IPC$ is unconfirmed.
- Checks for the host: `testparm -s` output of the real file, and whether the connected IP is in the `interfaces` list. A connection to an IP not in the list would be refused rather than answered with 67.

**restrict anonymous = 2, disable netbios, durable handles = no**
- `restrict anonymous = 2` blocks anonymous SAMR/LSA and anonymous connections to IPC$. An authenticated `net use ... /user:` is not affected. An unauthenticated `net use \\ip\IPC$` is refused with an access or logon error, not 67. [restrictanonymous.xml] A guest-ok share removes the effect.
- `disable netbios = yes` plus `smb ports = 445` is the standard SMB2-over-TCP setup. It only affects NetBIOS name resolution and port 139. Windows uses 445 directly, and E16 worked with it. I found nothing in the docs tying it to error 67.
- `durable handles = no` affects only handle reconnect. It does not affect tree connect. I found nothing relevant to 67. The parameter was not checked in 4.22 docs, so this is from memory.
- None of the three is a known cause. Bisecting them is cheap (remove the three lines and retry), but E16 already had `restrict anonymous = 2`.

**Signing and encryption**
- `server signing` default is disabled (i.e. auto) for every role except AD DC (mandatory). SMB2 signing cannot be turned off. Disabled behaves as auto, and mandatory still requires signing. [serversigning.xml]
- A client that requires signing, such as Windows 11 24H2, gets signed sessions from Samba (E16 shows AES-128-GMAC on SMB 3.1.1). No config change is needed.
- `server smb encrypt` default is `default`, which means off unless the share or global sets it. Windows only encrypts if the server demands it. Neither side blocks the other. Signing and encryption are not a plausible cause of 67, which is a tree connect name error. A signing failure would show as an access or session error.

**Samba log on the storage VM**
- Config writes `log file = /var/log/samba/log.%m`, so there is one file per client machine name, with `max log size = 1000` (kB). With `disable netbios` the %m is often the client IP or name. No `log level` is set, so the default 0 logs very little.
- I found no existing path in the project (agent API, hlab commands, MCP tools, `smb_linux.go`) that reads `/var/log/samba` or runs journalctl or smbstatus. The agent only runs testparm, pdbedit and `smbcontrol smbd reload-config`. E16 used `smbstatus -b` only on a throwaway VM. So reading the log needs new access, which the rules forbid as a debug way in. Only the e2e test can observe the failure through the Windows client output itself, or the storage VM serial or console if the project already exposes one. I did not check this.
- Ways without a new door: set `log level = 2` in the generated global config as a normal product setting, and read it where the project already reads agent output. This does not exist yet, so it counts as a change.

**Confirmed or unclear**
- server multi channel support default: confirmed (yes on Linux since 4.15).
- Interface list contents under `bind interfaces only`: unclear, the `load_interfaces()` filtering was not visible.
- Channel connections and `%i` config used on tree connect: unclear.
- `%i` evaluation per connection: confirmed by design (per smbd process), exact code point not confirmed.
- restrict anonymous, disable netbios, durable handles: restrict anonymous confirmed from docs. The other two found nothing, durable handles from memory only.
- Signing and encryption defaults: confirmed.
- Reading the Samba log with existing means: confirmed that no such means exists in the project.
## Windows net use semantics

Status: written from knowledge of the SMB redirector's documented behaviour. I did not run experiments or re-fetch the sources in this session, so each item is marked confirmed or unclear below.

1. Error 1219 key (unclear, probably the name string).
   - ERROR_SESSION_CREDENTIAL_CONFLICT (1219) means "multiple connections to a server or shared resource by the same user, using more than one user name, are not allowed". Microsoft's own `net use` troubleshooting guidance and many admin reports say the usual workaround is to reach the server under a different name (hostname, FQDN, IP or a DNS alias). That only works if the redirector keys on the server name as typed, not on the resolved address.
   - So a box name and a subaccount name that resolve to the same IP are most likely treated as two different servers and can hold different credentials side by side.
   - I found no Microsoft statement that says this precisely. Verify it once on the real hlab setup before depending on it.
   - Safe fallback: always `net use \\<name> /delete /y` (all shares of that name) before mapping with other credentials, and use one distinct server name per account.

2. Deviceless mappings (confirmed in principle).
   - `net use \\srv\share <pw> /user:acct /persistent:no` without a drive letter creates a deviceless connection. It lives only in the current logon session and is not restored at the next logon.
   - `/persistent:no` also keeps it out of the registry (`HKCU\Network`). No Credential Manager entry is made unless `/savecred` is used.
   - `net use \\srv\share /delete /y` removes the tree connect even with open handles. When the last tree connect to that server name for the logon session goes, the session's credentials go with it.
   - The underlying SMB transport connection can linger briefly (the idle `KeepConn` timeout, default 600 s). A very quick re-map with different credentials to the same name can therefore sometimes still return 1219 or a stale session. A short retry with a deadline, or a different name, avoids this.
   - Use `net use` with no arguments, or `Get-SmbConnection` or `Get-SmbMapping`, to assert in teardown that nothing is left.
   - Run cleanup in a deferred function that also runs on test failure. Also delete any leftover mapping before a run starts, in case a previous run crashed.

3. Go os.WriteFile and os.ReadFile on `\\srv\share\file` (confirmed in principle).
   - Go calls CreateFile on the UNC path. The redirector (MUP, then mrxsmb) picks the connection by the caller's logon session (LUID) and the server name in the path. It reuses the existing session, so it uses the credentials from `net use` in that logon session.
   - Go needs nothing extra. The path must use the exact same server name string as in `net use`, not the IP or an alias, or the name may be keyed as a different server (see 1) and fall back to the current user's own credentials, which would fail with access denied.
   - Go needs a `\\srv\share\...` path. Forward slashes also work.

4. Visibility under UAC split tokens (confirmed).
   - A mapping belongs to the logon session. All non-elevated processes of the same interactive user share one logon session, so they all see the mapping. This includes the `task` process and the Go test binary it starts.
   - The elevated token of the same user has a different, linked logon session. The elevated and non-elevated halves do not see each other's mappings unless `EnableLinkedConnections` is set, which is not the default and not needed here.
   - Processes under another logon, such as a service (for example `hlab-dev`), a scheduled task, `runas` or an SSH session, do not see the mapping. Do the `net use` and the file I/O in the same non-elevated test process tree, as the plan intends.
   - If the test is ever started from an elevated shell, `net use` and the Go I/O would still match each other, but they would not match a non-elevated Explorer.

Sources: Microsoft Learn `net use` reference (https://learn.microsoft.com/windows-server/administration/windows-commands/net-use); System Error Codes (1219) (https://learn.microsoft.com/windows/win32/debug/system-error-codes--1000-1299-); the UAC and `EnableLinkedConnections` note (https://learn.microsoft.com/troubleshoot/windows-client/networking/mapped-drives-not-available-from-elevated-command).
