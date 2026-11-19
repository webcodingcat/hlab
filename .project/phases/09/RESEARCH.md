# Phase 09 research

Date: 2026-11-19. No experiment was run in this session (no shell available); results come from documentation and the module source.

## 1. Set-VM -AutomaticStopAction by VM state

Status: UNCLEAR (not confirmed).

- The Microsoft docs for Set-VM state no state restriction for `-AutomaticStopAction` (values TurnOff, Save, ShutDown). Example 1 sets it with no remark about state. Source: https://learn.microsoft.com/en-us/powershell/module/hyper-v/set-vm
- Nothing in the docs says it works or fails on a Running or Saved VM. Memory only: Hyper-V Manager lets you edit the automatic stop action on a running VM, because it is a host-side VM setting, not hardware. For a Saved VM it is unknown.
- Recommendation: first e2e probe on a throwaway VM. Run `Set-VM -AutomaticStopAction X` on a Running VM and on a Saved VM, then read `(Get-VM).AutomaticStopAction`. Until it is probed, apply the change to Off VMs only, or treat a failure on Running/Saved as "applies at next stop".

## 2. Windows shutdown order and early shutdown detection

Status: partly confirmed.

- Documented: at user shutdown, every service that accepts SERVICE_ACCEPT_PRESHUTDOWN receives SERVICE_CONTROL_PRESHUTDOWN. The SCM waits until the service stops or its preshutdown timeout expires. Only after all preshutdown notifications are done do SERVICE_CONTROL_SHUTDOWN handlers run. `PreshutdownOrder` (multistring under `HKLM\SYSTEM\CurrentControlSet\Control`) sets the order. Source: https://learn.microsoft.com/en-us/windows/win32/services/service-control-handler-function
- Not stated in the docs: that the SCM waits for the first service (vmms) to finish before it sends PRESHUTDOWN to the next. The docs only say the SCM "waits until the service stops or the timeout expires" per service, and the `PreshutdownOrder` value exists for ordering. Sequential delivery in list order is the intended meaning, but the docs do not spell it out. E11 (.project/research/EXPERIMENTS.md) read the registry order only. It did not test a real Windows shutdown (LIFE-05 is manual).
- Services not in `PreshutdownOrder` are notified after the listed ones (not stated explicitly in the docs; unverified).
- Early detection from a session-0 service: nothing found in the docs. `GetSystemMetrics(SM_SHUTTINGDOWN)` is per-session (it is true only in a session that is shutting down), so it is not a reliable signal for a service in session 0. The documented, supported signals are the service's own SERVICE_CONTROL_PRESHUTDOWN and SERVICE_CONTROL_SHUTDOWN. A service can also get WM_QUERYENDSESSION through a hidden window only in an interactive session, and session 0 has no such path. No registry or WMI "shutdown started" flag was found in the documentation. Unclear whether any exists.
- Practical consequence: the status watch cannot know in advance. Within the PRESHUTDOWN handler, stop the watch writer first (set a flag before any state write) and treat VM state seen after the flag as untrusted. VM states that vmms changes before hlabd's PRESHUTDOWN arrives cannot be told apart from a user action unless the order guarantee holds. Verify with a real shutdown or a log of the handler timestamp against the VM state changes.

## 3. modernc.org/sqlite v1.62.0, non-database file

Status: confirmed from source; not run.

- The driver's error type is `*sqlite.Error` with `Code() int` (error.go). Result codes are in `modernc.org/sqlite/lib` (`sqlite3.SQLITE_NOTADB` = 26, `sqlite3.SQLITE_CORRUPT` = 11). `ErrorCodeString` has an entry for SQLITE_NOTADB: "File opened that is not a database file (SQLITE_NOTADB)".
- Timing: `sql.Open` only validates the driver name and the DSN and opens no connection. The file is read at the first connection. The driver opens the file with `sqlite3_open_v2`, which does not read the header, and then runs the DSN `_pragma` values. The store DSN has `journal_mode(WAL)`, `foreign_keys(1)` and `busy_timeout(5000)` (internal/store/store.go lines 61-63), and `journal_mode(WAL)` reads page 1. So the error most likely comes from the first Ping, query or Exec (the connection open), not from `sql.Open`, and the pragma step may wrap or replace the later `PRAGMA user_version` error. Expected code: SQLITE_NOTADB (26), message "file is not a database". SQLITE_CORRUPT (11) is more likely for a real SQLite file that is damaged or truncated. Not run, so the exact code and the point of failure are unverified.
- Detection in Go:
  ```go
  var se *sqlite.Error
  if errors.As(err, &se) && (se.Code()&0xff == sqlite3.SQLITE_NOTADB || se.Code()&0xff == sqlite3.SQLITE_CORRUPT) { ... }
  ```
  (`errors.As` already exists in store.go line 131 for UNIQUE.) Mask with `&0xff` for extended codes. Do not match on the message text.
- Pitfall: an empty (0-byte) file is a valid empty database and gives no error. A non-empty file with fewer than 100 bytes of junk is also reported as NOTADB by SQLite in practice (memory, unverified).
- Version: the module is v1.62.0 in go.mod. The latest version was not checked in this session.
- Suggested check: a short Go test that writes junk to a temp file, opens it with the store DSN and prints the error and `Code()` at Open, Ping and the first Exec.
