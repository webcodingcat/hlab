# The service, the lab control and the CLI

How `hlabd` starts and stops, what it does on the machine (the host link,
the appliance, its watchdog and its update), and the lab control, the CLI
and the web UI. The rules are in DEVELOPING.md; section names in
parentheses point to this file unless they name another.

## The service

`cmd/hlabd` puts the parts together (`service.go`). It may import anything; the
packages below it follow the layers.

- **Start** (`start`) does these steps in order:
  1. Read `hlab.yaml` (`internal/config`). A missing file or key takes the default.
     An unknown key or a value out of range stops the start, and the error names the
     file.
  2. Load the catalog.
  3. `lab.Open`. It takes the data folder for itself through `Lifecycle.Lock`, so
     only one HLab runs on a machine.
  4. `Lab.StartMachine` (The machine's side, below): the host link, the
     appliance and its pictures. Ctrl+C ends a start that waits for them.
  5. Open the ports.
  6. Create the project `default` if the lab is new (`Lab.Fresh`: there was no state
     file). This comes after the ports, so a port that is already taken leaves a new
     lab untouched.
  7. Write a new control token through `Lifecycle.WriteSecret`.

  On Windows the three ports listen on the host link's address `198.18.0.1`, and
  the lab control also on `127.0.0.1:8090`; with `--platform fake` all listen on
  `127.0.0.1`. A port of 0 takes any free port (the tests do that), and the status
  reports the ports in use.
- **Ports:** the Cloud API on 8080, the Hetzner API on 8081 (no routes until the
  Storage Boxes, phase 5), and 8090 for the lab control under `/_lab` and the web UI.
- **Stop** (`close`). The service stops on `POST /_lab/shutdown`, on
  `Lifecycle.StopRequests` (Ctrl+C, SIGTERM) or when a server fails. Then:
  1. The status says `stopping`; every new request gets 503 `unavailable`, as
     Hetzner's API answers while it is down, but for the lab control's status and
     shutdown. The requests that came before finish (up to 10 s).
  2. `Lab.Close`: the actions end, the appliance shuts down (30 s, then off), the
     host's DNS rules and its link go, and the state is written. The lab control
     still answers meanwhile.
  3. The servers stop, and the control token file is removed, so the CLI knows
     the lab is gone.
- **`--platform fake`** uses the fake machine but this machine's real life cycle:
  the lock, the token file and the signals are real. So only one HLab runs, and the
  CLI finds it. The fake machine has a fake agent (`fake.Agent`) in each of the
  appliance's VMs, and a stand-in release disk in the data folder when there is
  no real one, so `StartMachine` runs there as well.
- **The appliance's release disk** is `hlab-appliance.vhdx` next to `hlabd`, or
  `--appliance <path>`. A start copies it to `<data>/appliance/<sha256>.vhdx`
  unless that is there; its SHA-256 is the release's ID. The appliance runs on a
  differencing disk of its own on the copy, `<data>/appliance/system-<A|B>.vhdx`.
  So the release disk next to `hlabd` is never held open (Hyper-V holds a
  differencing disk's parent), and an update of HLab can replace it while the
  appliance runs on.
- **The life cycles:**
  - Windows holds the mutex `Global\HLab`. It writes the path of its data folder to
    `%ProgramData%\HLab\instance`, so a second start can say in its error message
    which folder is in use and the CLI finds the lab's token; it removes the
    file when it ends (a crash leaves it until the next start). The live tests
    write their own. It writes the token
    with its own ACL: local users may read it.
  - Linux holds an flock on `/run/lock/hlab.lock`; that file contains the path of
    the data folder. It writes the token with mode `0640` and the group `hlab`, if
    the machine has that group.
- **The log** goes to stderr as text and to `logs/hlabd.log` in the data folder
  as JSON lines (`cmd/hlabd/logfile.go`): hlabd mostly runs without a console
  (the start menu's minimized window, `hlab start`, the dev loop's task). A
  start moves a log of 10 MB or more to `hlabd.log.1`.

### The machine's side

`Lab.StartMachine` starts the lab's side on the machine; `Lab.Close` undoes
it. `system` does each step, `lab` only builds the pictures.

1. `system.ConnectHost`: `HostNet.CheckRanges` refuses to start if a route or an
   address outside the lab's switches overlaps `198.18.0.0/15` or
   `2001:db8::/32`, naming the range and the interface. Then `HostNet.SetUp`
   creates or takes over the switches `HLab-Link` (internal, with the host's NIC:
   `198.18.0.1/29`, `2001:db8:0:ffff::1/64`, routes of both ranges through the
   appliance) and `HLab-Public` (private).
2. `system.StageApplianceRelease`: the release disk into the data folder (above).
3. `system.RemoveStrayVMs`: the lab's VMs (notes `hlab`) that are neither the
   appliance of the state's MAC set nor a server of the state go; their disks
   stay.
4. `Appliance.Start`: an appliance that runs and whose agent answers is taken
   over (after a crash of hlabd); otherwise its VM and its differencing disk
   are made anew on the new release and started. Then every picture goes to the
   agent, and the watchdog starts. A taken-over appliance of another release
   than the new one is updated (below).
5. `system.SetHostDNS`: the host's DNS rules; until the zones (2b) only the
   rules of an earlier run go. Then the appliance's folder loses what the
   running appliance and the new release do not need.
6. The servers come back as they want (docs/servers.md), and their
   watchdog starts. A server that cannot come back (a broken disk, an image
   that does not load) is logged and stays off; it still wants to run, so the
   next start tries again.

If a step fails, the host link is removed again. A crash of hlabd leaves everything
running; the next start takes it over.

**The watchdog** checks every 2 s that the appliance's VM runs, its heartbeat
answers and its agent answers; on the way it sends the pictures that the agent
lacks (an agent that restarted alone). After three failed checks in a row, or
at once when the VM is off or gone, it turns the VM off and on again with the
same MAC set and sends every picture. A picture that the agent refused (422) is
not sent again until its generation changes.

**The update** (`Lab.updateAppliance`) replaces a running
appliance of an older release, which a start took over, without stopping the
servers. The state (`Lab.Appliance`) names the running appliance's release and
MAC set; its VM is `hlab-appliance-a` or `hlab-appliance-b`.

1. The new appliance starts beside the old one with the other MAC set, its NICs
   on the same switches, and gets every picture, built for its set. Its agent
   announces the addresses after the `links` picture, so the neighbors move to
   it while the old one still answers.
2. The state names the new appliance.
3. The old one is turned off and deleted, with its system disk.
4. The new one gets `links` again and announces once more: the old one had
   answered ARP and ND too while both ran.

The servers' connections to the host and to each other live through it;
those to the Internet break, because the new appliance has another uplink
address and none of the old one's NAT state; the watchdog's restart
loses that state as well.

If the new appliance does not come up, it is deleted and the old one runs on; the
next start tries again. HLab's end during an update leaves both appliances
running; the next start keeps the one the state names (the old one before step
2, the new one after it), removes the other as a stray VM and announces again
with the pictures. Measured on a Ryzen AI Max+ 395: the update takes 11 s
(the new appliance boots in 9 s); a server took no new connection for 0.5 s
at most, and an open SSH session did not stall.

## Lab control, CLI and web UI

- **The lab control** lives in `internal/api/control`; its types are in
  `internal/labapi`. Its routes:
  - `GET /_lab/status` and `POST /_lab/shutdown`;
  - projects: `GET|POST /_lab/projects`, `DELETE /_lab/projects/{name}`;
  - tokens: `GET|POST /_lab/projects/{name}/tokens`, `DELETE …/tokens/{token}`,
    where `{token}` is the token's name;
  - the env token: `POST /_lab/projects/{name}/env-token`. It is a POST because the
    first call creates the token;
  - `GET /_lab/projects/{name}/servers/{server}/ssh`, for `hlab ssh`: the
    server's address (Primary IPv4, else the `::1` of its IPv6 network), the
    user `root` and the lab's private key;
  - the Cloud API for a project: `/_lab/projects/{name}/cloud/v1/...`. It runs the
    Cloud API's handlers through `API.ServeAs` with `read_write`.

  It accepts the control token (`Authorization: Bearer`) or whatever
  `Options.Admit` vouches for (the web UI's cookie). Errors have Hetzner's format
  (`web.WriteError`): the lab's `ErrInvalid` is 422 `invalid_input`, `ErrExists` is
  409 `uniqueness_error`, `state.ErrNotFound` is 404 `not_found`.
- **The client** (`internal/client`) finds the token in `HLAB_TOKEN_FILE`. Without
  it, it reads `control.token` in the data folder that the instance file names
  (`client.DataDir`), so it finds the dev loop's lab in `C:\HLabDev\data` as
  well as an installed one:
  - `C:\ProgramData\HLab\instance` on Windows;
  - `/mnt/c/ProgramData/HLab/instance` in WSL2 (detected through
    `WSL_DISTRO_NAME`), with the folder turned into its `/mnt/<drive>` path;
  - without the file, or for a folder not on a drive, the default folder;
    on Linux always `/var/lib/hlab`.

  It calls the address in `HLAB_CONTROL`, or else `http://198.18.0.1:8090`, the host
  link that Windows and WSL2 share. If there is no token file or no answer, it
  returns `client.ErrNotRunning`. A connection gives up after 2 s: once the lab
  has stopped, `198.18.0.1` is gone, and from WSL2 a connection to it would hang
  until the request's timeout of 30 s. An error answer becomes a `*client.Error` with
  its status and code, so the CLI can give a hint (for 401: the token file
  belongs to another `hlabd`).
- **The CLI** (`internal/cli`, cobra) has one file per group of commands; each
  command calls the client. `hlab env` prints the endpoints and the project's env
  token for sh (`export`) or PowerShell (`$env:`), in single quotes.
  - `hlab start` runs `hlabd.exe` with administrator rights and without a window
    (`Start-Process -Verb RunAs -WindowStyle Hidden` in `powershell.exe`, which
    WSL2 reaches through interop): on Windows the one next to `hlab.exe`, in
    WSL2 the installed one in `C:\Program Files\HLab`. Then it asks the status
    every half second, reading the token file anew each time, until the lab
    runs (at most 3 minutes).
  - `hlab stop` sends `POST /_lab/shutdown` and waits the same way until the
    lab no longer answers (`--no-wait` does not).
  - `hlab ssh SERVER [COMMAND...]` gets the address and the lab's key from the
    lab control, writes the key to a temporary file of its own (ssh refuses a
    key that others may read, as the data folder's copy is) and runs `ssh`
    (OpenSSH, also Windows' own) without known hosts: a server's address
    comes back with a new host key after each create. Its exit code is
    ssh's.
- **The web UI** (`internal/webui`) is `index.html`, `ui.js` and `ui.css` in
  `files/`, built in with `go:embed`, without a build step.
  - `UI.Guard` runs before everything else on port 8090. The Host header must be
    one of the port's addresses (or `localhost` for a loopback address); this
    prevents DNS rebinding.
  - A change (any method except GET, HEAD, OPTIONS) needs an `Origin` header of the
    page itself or, without `Origin`, a token; this prevents CSRF.
  - The page sets a random cookie (`HttpOnly`, `SameSite=Strict`, new at each
    start). The lab control accepts it instead of the token (`UI.Admits`), so the
    browser never holds a token.
  - The page reads the catalog through the project's Cloud API route.
- **Who reaches port 8090.** The cookie grants the same rights as the control token,
  to anyone who loads the page. That is the same group of people:
  - Port 8090 listens on `127.0.0.1` and the host link, which only the host and
    WSL2 reach.
  - The control token is readable for the host's local users anyway.
  - Guests never reach the port: the appliance's ruleset lets them reach the host
    link only on 8080 and 8081.
