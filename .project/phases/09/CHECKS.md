# Phase 09 manual checks

## LIFE-05 A Windows shutdown or restart with the cloud running applies the stop behaviour

- Start: the dev cloud is running (`bin\hlab.exe cloud status` shows `running`); a PowerShell window in the repository root.
- Setup: one server that runs, created for example with `hcloud --context hlab-docs server create --name life05 --type cx23 --image ubuntu-24.04 --location fsn1 --ssh-key <your key>`; note its uptime with `ssh root@<its IPv4> uptime -s`. `stop_behaviour` in `C:\ProgramData\hlab-dev\settings.toml` is `save`; if you change it, run `bin\hlab.exe cloud stop` and `bin\hlab.exe cloud start` once afterwards, because the setting reaches the VMs only at a cloud start (Hyper-V refuses it on running VMs). Claude can create the server and note the uptime for you.
- Steps: 1. Restart Windows from the start menu while the cloud runs. 2. After logging in again, wait until `bin\hlab.exe cloud status` shows `running` (start the cloud with `bin\hlab.exe cloud start` if it is not set to start on its own). 3. Run `ssh root@<its IPv4> uptime -s` again. 4. Optional: set `stop_behaviour = "shutdown"`, run `cloud stop` and `cloud start`, then repeat steps 1 to 3.
- Expected: with `save`, the server is running again and `uptime -s` shows the same time as before (it was saved, not rebooted). With `shutdown`, the server is running again with a later `uptime -s` (it was shut down and powered on). In both cases `bin\hlab.exe logs` shows no error about restoring servers, and `route print` shows the route of the public range only while the cloud runs.
- Result: open
