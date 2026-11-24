# Faults

The faults that are active in this project. A fault makes the project's
cloud misbehave on purpose, for example a crashed server or failing API
requests, so you can test how your setup copes. Faults can only be
managed while the cloud runs. The list refreshes every 5 seconds.

- The list shows each fault's **Kind**, its **Target** (a link to the
  server, volume or storage box; "Project" for API errors and rate
  limits; "fsn1 ↔ hel1" for latency between two locations), its
  **Parameters** in one line, its **Seed** and the **Remaining** time
  ("4 min left", or "until ended" without a duration).
- **End** in the row menu (`…`) ends that fault at once.
- **End all** ends every fault of this project after you confirm.
- **Create fault** opens the create form.

## Create a fault

Choose the **Kind**; the line below it explains what the kind does, and
the form shows only the fields that kind takes:

- **Target**: the server, attached volume, storage box, or for a slow
  disk a server or a volume. When the project has nothing that fits, the
  select says so and **Create fault** stays disabled.
- **Latency on**: **Server** or **Between locations** with a **From** and
  a **To** location; then **Delay (ms)** and **Jitter (ms)**.
- **API error**: the **Error code** with its HTTP status, the
  **Percent** of requests that fail, **Only** (Reads, Writes or All) and
  the **Resource type** (All or one kind of resource).
- **Rate limit**: **Requests per hour**.
- **Network**: **Traffic** (Public, Private or All) and **Loss percent**;
  100 cuts the server off.
- **Slow disk**: **IOPS**.
- **Seed**, for API errors and for network faults below 100 % loss: the
  same seed makes the same requests or packets fail. Leave it empty and
  HLab chooses one and shows it in the list.
- **Duration**: **Until ended**, 1, 5, 15 or 60 minutes, or **Custom**,
  typed as `90s`, `10m` or `2h` (1 s to 24 h).

A value HLab refuses shows its reason under the field. **Cancel** returns
to the list.

Faults is an entry of the sidebar, below **Security**. The top bar shows
"N faults active" while any project has a fault; it opens **Active
faults**.
