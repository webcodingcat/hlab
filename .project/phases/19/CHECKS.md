# Phase 19 manual checks

## COMPACT (19, user request) A deleted box gives its space on the host back after a cloud stop

The storage VM's data disk `data.vhdx` is dynamic. When a box is deleted
and the cloud is stopped, hlab compacts the disk, so the host drive gets
the space back.

- Start: PowerShell in `C:\dev\WebCodingCat\hlab` with the dev service
  `hlab-dev` running (`bin\hlab.exe` talks to it).
- Setup:
  - In `C:\ProgramData\hlab-dev\settings.toml` set
    `stop_behaviour = "shutdown"` (with "save" the compaction is skipped
    and logged). Then run `bin\hlab.exe cloud stop` and
    `bin\hlab.exe cloud start`.
  - Create two storage boxes through the Hetzner API
    (`http://api.hetzner.internal/hetzner/v1`, for example
    `hcloud storage-box create`): box A with `reachable_externally` and
    `ssh_enabled` on, and box B. Box B keeps the storage VM alive:
    deleting the last box removes the VM and the whole `storage\`
    folder.
- Steps:
  1. In WSL run `head -c 2G /dev/urandom > /tmp/2g.bin` and upload it with
     `sftp u<n>@u<n>.storagebox.hetzner.internal` and `put /tmp/2g.bin`,
     using box A's password.
  2. Run `bin\hlab.exe cloud stop` and note the size of the disk:
     `(Get-Item C:\ProgramData\hlab-dev\data\storage\data.vhdx).Length`.
     Run `bin\hlab.exe cloud start`.
  3. Delete box A and wait until its action has finished.
  4. Run `bin\hlab.exe cloud stop` and read the size again.
  5. Afterwards set `stop_behaviour` back to its old value ("save") and
     delete box B.
- Expected: after step 2 the file is about 2 GB larger than an empty
  disk; after step 4 it is clearly smaller again, roughly as before the
  upload. The hlabd log in `C:\ProgramData\hlab-dev\logs` shows
  "data disk of the storage VM compacted" with its duration.
- Result: open
